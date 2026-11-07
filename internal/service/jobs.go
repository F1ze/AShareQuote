// Package service 编排采集、归档、入库与任务恢复，不直接实现供应商协议或数据库事务。
// 网络客户端、线路选择和存储均通过小接口注入，供网页与批处理入口复用同一套用例。
package service

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"quotes/internal/domain"
)

// Client 只暴露采集所需的协议能力，任务层无需依赖完整的通达信实现。
type Client interface {
	Close()
	SecurityCount(market uint8) (int, error)
	SecurityList(market uint8, start, count int) ([]domain.Security, error)
	SecurityBars(category int, market uint8, code string, start, count int) ([]domain.Bar, error)
}

type ClientFactory func(hosts []domain.Host, timeout time.Duration, offset int) Client

// JobService 管理单个活动任务；互斥检查与发布任务必须连续完成，防止两个请求同时启动。
// 这也避免多个任务同时合并同一 CSV，数据库的跨进程单写者约束则由入口的文件锁保障。
type JobService struct {
	configureTraffic func(int, int)        // 应用共享限流器的策略更新端口，由组装根注入，不执行协议细节。
	mu               sync.RWMutex          // 保护当前任务指针及“检查无活动任务→发布新任务”的临界区。
	current          *jobRun               // 最近一次任务，结束后仍保留供界面查看；nil 表示尚无任务。
	defaultOut       string                // 未指定 OutputDir 时使用的归档目录，不是 DuckDB 文件路径。
	provider         LineProvider          // 注入给通达信任务的线路探测服务。
	newClient        ClientFactory         // 注入给通达信任务的客户端构造函数。
	archiveFactory   func(string) Archive  // 按任务输出目录构建归档器，由组装根选择实现。
	factorRepository FactorRepository      // 由入口注入的独立因子仓库。
	warehouse        Warehouse             // 应用级仓库实例；每项任务是否使用它由 Storage 决定。
	journal          JobRepository         // 跨任务持久日志存储，由组装根注入。
	localSource      LocalSource           // 应用级本地历史读取器，启动任务时传入任务实例。
	historySources   map[string]SourceSpec // 历史源注册表，键为 Source；仅启动组装阶段写入，运行期只读。
}

// JobDependencies 是任务服务的组装输入；实现均从入口注入，服务不导入任何存储或协议实现。
type JobDependencies struct {
	ConfigureTraffic func(int, int)       // 可选通达信全局/单站频率配置端口，任务探测前调用。
	DefaultOutput    string               // 缺省原始归档目录，与数据库目录分开。
	Lines            LineProvider         // 通达信线路服务，纯本地/历史源测试可为 nil。
	NewClient        ClientFactory        // 每个 worker 独占的协议客户端工厂。
	Archives         func(string) Archive // 按输出目录建立归档仓库，必填。
	Factors          FactorRepository     // 独立因子附件读写端口，仅所选来源/选项需要时使用。
	Jobs             JobRepository        // 持久任务记录；nil 时禁用历史恢复。
	Warehouse        Warehouse            // 可选幂等入库端口。
	Local            LocalSource          // 可选本地解码来源。
}

// NewJobService 仅保存依赖，不读写文件；Restore 显式加载已有任务状态。
func NewJobService(d JobDependencies) (*JobService, error) {
	if d.Archives == nil {
		return nil, fmt.Errorf("未配置原始归档仓库")
	}
	return &JobService{configureTraffic: d.ConfigureTraffic, defaultOut: d.DefaultOutput, provider: d.Lines, newClient: d.NewClient, archiveFactory: d.Archives,
		factorRepository: d.Factors, journal: d.Jobs, warehouse: d.Warehouse, localSource: d.Local}, nil
}

func (m *JobService) DefaultOutput() string { return m.defaultOut }

func (m *JobService) OutputDir() string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if m.current != nil {
		return m.current.output
	}
	return m.defaultOut
}

func (m *JobService) currentRun() *jobRun {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.current
}

func (m *JobService) Snapshot() *TaskSnapshot {
	task := m.currentRun()
	if task == nil {
		return nil
	}
	snapshot := task.snapshot()
	return &snapshot
}

func activeState(state string) bool {
	return state == "preparing" || state == "running" || state == "cancelling"
}

func (m *JobService) Start(options DownloadOptions) (TaskSnapshot, error) {
	return m.start(options, nil)
}

// start 统一校验网页、批处理和恢复入口的选项，再创建独立任务生命周期。
// 校验与起始日志成功前不发布任务，避免启动失败后留下无法运行的活动状态。
func (m *JobService) start(options DownloadOptions, resume *savedRun) (TaskSnapshot, error) {
	// 第 1 步：持 JobService 锁检查活动任务；直到新任务发布前不释放，避免并发启动覆盖 current。
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.current != nil && activeState(m.current.snapshot().State) {
		return TaskSnapshot{}, errors.New("已有下载任务正在运行，请先取消或等待完成")
	}
	// 第 2 步：领域统一处理默认值、日期/单位/代码约束；服务只检查本环境所需能力是否已经注入。
	var err error
	options, err = domain.NormalizeDownloadOptions(options, m.defaultOut, time.Now())
	if err != nil {
		return TaskSnapshot{}, err
	}
	if options.Storage == "duckdb" && m.warehouse == nil {
		return TaskSnapshot{}, errors.New("未配置 DuckDB 数据仓库")
	}
	spec, remote := m.historySources[options.Source]
	if options.Source != "tdx" && !remote {
		return TaskSnapshot{}, errors.New("未注册的数据来源")
	}
	if remote {
		if options.StartDate == "" {
			return TaskSnapshot{}, errors.New("历史来源需要明确日期范围")
		}
		if !slices.Contains(spec.Categories, options.Category) {
			return TaskSnapshot{}, fmt.Errorf("%s 不支持该周期", spec.Label)
		}
		for _, market := range options.Markets {
			if !slices.Contains(spec.Markets, market) {
				return TaskSnapshot{}, fmt.Errorf("%s 不支持该市场", spec.Label)
			}
		}
	}
	if options.ImportDir != "" && m.localSource == nil {
		return TaskSnapshot{}, errors.New("未配置本地历史读取器")
	}
	if strings.TrimSpace(options.Codes) != "" {
		if remote {
			securities, _ := domain.ParseCodes(options.Codes)
			for _, security := range securities {
				if !slices.Contains(spec.Markets, int(security.Market)) {
					return TaskSnapshot{}, fmt.Errorf("%s 不支持该证券市场", spec.Label)
				}
			}
		}
	}
	// 第 3 步：准备输出目录，随后构造独立任务上下文和所需依赖。
	output, err := filepath.Abs(options.OutputDir)
	if err != nil {
		return TaskSnapshot{}, fmt.Errorf("输出目录无效：%w", err)
	}

	options.OutputDir = output
	// 任务不能绑定启动它的 HTTP 请求：请求返回或页面关闭后仍应继续运行。
	// 生命周期由显式取消和应用 Shutdown 控制，网络请求再继承这个任务上下文。
	ctx, cancel := context.WithCancel(context.Background())
	task := &jobRun{
		configureTraffic: m.configureTraffic,
		id:               time.Now().Format("20060102-150405.000000000"), state: "preparing", message: "正在检测可用行情线路",
		output: output, options: options, provider: m.provider, newClient: m.newClient,
		started: time.Now(), ctx: ctx, cancel: cancel,
		journal: m.journal, localSource: m.localSource, historySource: spec.Adapter, factorRepository: m.factorRepository,
	}
	// 恢复不复制旧任务的计数：新任务从 0 统计，以旧计划和哈希断点决定各项是否可复用。
	if resume != nil {
		task.resumeCompleted = resume.Completed
		task.resumePlan = resume.Plan
	}
	if m.archiveFactory != nil {
		task.archive = m.archiveFactory(output)
		if task.archive == nil {
			cancel()
			return TaskSnapshot{}, errors.New("归档仓库构造失败")
		}
		if err := task.archive.Prepare(); err != nil {
			cancel()
			return TaskSnapshot{}, fmt.Errorf("准备归档目录：%w", err)
		}
	}
	if options.Storage == "duckdb" {
		task.warehouse = m.warehouse
	}
	// 第 4 步：先记录可重建的起始状态，再发布到 JobService 并启动后台协程。
	// 起始日志写失败就取消上下文并返回，不能启动一个无法可靠恢复的任务。
	if task.journal != nil {
		s := task.snapshot()
		if err := task.journal.Append(task.id, runEvent{Kind: "start", Snapshot: &s}); err != nil {
			cancel()
			return TaskSnapshot{}, fmt.Errorf("创建任务日志：%w", err)
		}
	}
	m.current = task
	task.log("info", "使用内置站点模板，无需 connect.cfg；并发 worker 数 %d", options.Workers)
	go task.run()
	return task.snapshot(), nil
}

// Cancel 只请求停止，最终状态由执行协程在资源收尾后写入，不能立即宣称已停止。
func (m *JobService) Cancel() error {
	task := m.currentRun()
	if task == nil {
		return errors.New("当前没有可取消的任务")
	}
	task.mu.Lock()
	if !activeState(task.state) {
		task.mu.Unlock()
		return errors.New("当前没有可取消的任务")
	}
	task.state = "cancelling"
	task.message = "正在停止，等待进行中的请求退出"
	task.cancel()
	task.mu.Unlock()
	task.log("warn", "用户请求取消任务")
	return nil
}

func csvFileName(security Security, category int) string {
	return fmt.Sprintf("%s%s_%s.csv", marketPrefix(security.Market), security.Code, categoryName(category))
}
