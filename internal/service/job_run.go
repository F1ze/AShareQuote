package service

import (
	"context"
	"fmt"
	"sync"
	"time"

	"quotes/internal/domain"
)

// jobRun 的进度、日志和终态受 mu 保护；配置、计划及依赖在启动前确定。
// 修改进度后再调用 log/snapshot，避免持有写锁时重复获取同一个锁。
// 主要状态流程：preparing → running → completed / completed_with_errors / failed。
// 取消由 preparing/running → cancelling → cancelled；准备失败也可直接进入 failed。
// 重启从日志观察到未终结任务时标成 interrupted，用户恢复会创建独立的新任务。
type jobRun struct {
	configureTraffic func(int, int)               // 进入通达信流程前更新共享预算；本地/历史源和完整离线恢复不调用。
	lineReport       *domain.LineReport           // 受 mu 保护的本任务探测快照，成功与失败线路均保留供 SSE 展示。
	mu               sync.RWMutex                 // 保护可变进度、状态和展示切片；不覆盖网络或磁盘 I/O，避免锁长期占用。
	id               string                       // 本次执行编号，与对应的 <id>.jsonl 日志文件名一致。
	state            string                       // 当前状态；由调度流程决定终态，Cancel 只先标记 cancelling。
	total            int                          // 已固定计划的处理项数，网络按证券、本地按文件计。
	done             int                          // 已记录成功或失败结果的项数，取消未完成项不计入。
	success          int                          // 成功项数，含断点复用，对应快照 Succeeded。
	failed           int                          // 失败项数；单项失败通常不终止其他项的处理。
	bars             int64                        // 成功项本次读取量累加，重叠也计入；恢复复用不增加。
	pages            int64                        // 来源读取次数，具体口径见 TaskSnapshot.Pages。
	warnings         int                          // 带警告的成功项数，用于区分完成与带提示完成。
	skipped          int                          // 成功复用已校验断点的项数，作为 success 的子集。
	message          string                       // 阶段或结果说明，由快照传给界面。
	output           string                       // 当前任务的原始归档绝对目录，启动后不变。
	options          DownloadOptions              // 当前执行的固定选项；worker 要调整增量范围时使用副本。
	hosts            []Host                       // TCP 连通性检测通过后的端点列表，启动 worker 前一次性设置。
	started          time.Time                    // 任务创建时刻，用于计算总耗时和平均处理速度。
	ended            *time.Time                   // 结束时刻，活动任务为 nil；finish 设置后快照耗时固定。
	recent           []FileResult                 // 最近 10 个成功结果，头部插入；读取快照时复制。
	logs             []LogEntry                   // 最近 120 条业务日志；不承担断点恢复职责。
	factors          map[string][]AdjustmentEvent // 通达信 gbbq 事件，键为 market:code；nil 表示本次未载入，不应删除已有附件。
	provider         LineProvider                 // 通达信可用线路提供器；本地及完整归档恢复路径不需要访问它。
	newClient        ClientFactory                // 协议客户端工厂，各 worker 独占实例，避免报文串扰。
	ctx              context.Context              // 独立于 HTTP 请求的任务上下文，向网络、文件处理和桥接进程传播取消。
	cancel           context.CancelFunc           // 本次上下文的取消函数，用户取消、任务退出和应用关闭时调用。
	archive          Archive                      // 原始行情合并保存端口，默认 CSVArchive；先于仓库提交。
	factorRepository FactorRepository             // 原始因子文件读写实现，来源与编排不直接操作文件。
	warehouse        Warehouse                    // 当前任务的可选入库端口；仅 Storage=duckdb 时注入，nil 表示只归档。
	journal          JobRepository                // 持久事件日志；nil 表示未启用任务恢复，成功结果须在资源保存后记录。
	resumeCompleted  map[string]checkpoint        // 原任务的完成断点，键为归档文件名；读取后还必须验证内容哈希。
	localSource      LocalSource                  // 本地目录扫描和解码适配器，仅 ImportDir 非空时使用。
	historySource    HistorySource                // 注册的通用历史来源，nil 时网络采集走专用通达信流程。
	resumePlan       *ingestionPlan               // 原任务的稳定证券/文件计划，nil 表示新建任务，不能用新名单替换恢复范围。
}

func (t *jobRun) log(level, format string, args ...any) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.logs = append(t.logs, LogEntry{Time: time.Now().Format("15:04:05"), Level: level, Message: fmt.Sprintf(format, args...)})
	if len(t.logs) > 120 {
		t.logs = append([]LogEntry(nil), t.logs[len(t.logs)-120:]...)
	}
}

// snapshot 在同一次读锁内复制状态和切片，避免序列化期间与 worker 写入共享底层数组。
func (t *jobRun) snapshot() TaskSnapshot {
	t.mu.RLock()
	defer t.mu.RUnlock()
	elapsed := time.Since(t.started).Seconds()
	if t.ended != nil {
		elapsed = t.ended.Sub(t.started).Seconds()
	}
	percent := 0.0
	if t.total > 0 {
		percent = float64(t.done) / float64(t.total) * 100
	}
	speed := 0.0
	if elapsed > 0.05 && (t.state == "running" || t.state == "cancelling" || t.ended != nil) {
		speed = float64(t.done) / elapsed
	}
	s := TaskSnapshot{
		ID: t.id, State: t.state, Total: t.total, Done: t.done, Succeeded: t.success,
		Failed: t.failed, Bars: t.bars, Pages: t.pages, Warnings: t.warnings, Skipped: t.skipped, Percent: percent, Speed: speed, Elapsed: elapsed,
		Message: t.message, OutputDir: t.output, StartedAt: t.started, EndedAt: t.ended,
		Options: t.options, Recent: append([]FileResult(nil), t.recent...), Logs: append([]LogEntry(nil), t.logs...),
	}
	// 快照拥有独立结果切片，避免浏览器序列化或调用者改动触及内部状态。
	if t.lineReport != nil {
		report := *t.lineReport
		report.Results = append([]domain.HostStatus(nil), report.Results...)
		s.LineReport = &report
	}
	return s
}

func (t *jobRun) setState(state, message string) {
	t.mu.Lock()
	t.state = state
	if message != "" {
		t.message = message
	}
	t.mu.Unlock()
}

func (t *jobRun) finish(state, message string) {
	now := time.Now()
	t.mu.Lock()
	t.state = state
	t.message = message
	t.ended = &now
	t.mu.Unlock()
	if t.journal != nil {
		s := t.snapshot()
		if err := t.journal.Append(t.id, runEvent{Kind: "finish", Snapshot: &s}); err != nil {
			t.log("error", "保存任务终态失败：%v", err)
		}
	}
}
