// Package download coordinates quote retrieval, persistence and task state.
// Network and line-selection policies are injected through small interfaces.
package download

import (
	"context"
	"encoding/csv"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"quotes/internal/adjust"
	"quotes/internal/core"
	"quotes/internal/lines"
)

type Host = core.Host
type Security = core.Security
type Bar = core.Bar
type AdjustmentEvent = adjust.AdjustmentEvent

type DownloadOptions struct {
	Markets   []uint8 `json:"markets"`
	Codes     string  `json:"codes"`
	Category  int     `json:"category"`
	Start     int     `json:"start"`
	Count     int     `json:"count"`
	Workers   int     `json:"workers"`
	Timeout   int     `json:"timeout"`
	OutputDir string  `json:"outputDir"`
	GBBQPath  string  `json:"gbbqPath"`
}

// Client is the protocol surface needed by download workers.
type Client interface {
	Close()
	SecurityCount(market uint8) (int, error)
	SecurityList(market uint8, start, count int) ([]core.Security, error)
	SecurityBars(category int, market uint8, code string, start, count int) ([]core.Bar, error)
}

type ClientFactory func(hosts []core.Host, timeout time.Duration, offset int) Client

type LogEntry struct {
	Time    string `json:"time"`
	Level   string `json:"level"`
	Message string `json:"message"`
}

type FileResult struct {
	Name      string `json:"name"`
	Code      string `json:"code"`
	Market    string `json:"market"`
	Bars      int    `json:"bars"`
	Size      int64  `json:"size"`
	Duration  int64  `json:"durationMs"`
	CreatedAt string `json:"createdAt"`
}

type TaskSnapshot struct {
	ID        string          `json:"id"`
	State     string          `json:"state"`
	Total     int             `json:"total"`
	Done      int             `json:"done"`
	Succeeded int             `json:"succeeded"`
	Failed    int             `json:"failed"`
	Bars      int64           `json:"bars"`
	Percent   float64         `json:"percent"`
	Speed     float64         `json:"speed"`
	Elapsed   float64         `json:"elapsed"`
	Message   string          `json:"message"`
	OutputDir string          `json:"outputDir"`
	StartedAt time.Time       `json:"startedAt"`
	EndedAt   *time.Time      `json:"endedAt,omitempty"`
	Options   DownloadOptions `json:"options"`
	Recent    []FileResult    `json:"recent"`
	Logs      []LogEntry      `json:"logs"`
}

type DownloadTask struct {
	mu        sync.RWMutex
	id        string
	state     string
	total     int
	done      int
	success   int
	failed    int
	bars      int64
	message   string
	output    string
	options   DownloadOptions
	hosts     []Host
	started   time.Time
	ended     *time.Time
	recent    []FileResult
	logs      []LogEntry
	factors   map[string][]AdjustmentEvent
	provider  lines.Provider
	newClient ClientFactory
	ctx       context.Context
	cancel    context.CancelFunc
}

func (t *DownloadTask) log(level, format string, args ...any) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.logs = append(t.logs, LogEntry{Time: time.Now().Format("15:04:05"), Level: level, Message: fmt.Sprintf(format, args...)})
	if len(t.logs) > 120 {
		t.logs = append([]LogEntry(nil), t.logs[len(t.logs)-120:]...)
	}
}

func (t *DownloadTask) snapshot() TaskSnapshot {
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
		Failed: t.failed, Bars: t.bars, Percent: percent, Speed: speed, Elapsed: elapsed,
		Message: t.message, OutputDir: t.output, StartedAt: t.started, EndedAt: t.ended,
		Options: t.options, Recent: append([]FileResult(nil), t.recent...), Logs: append([]LogEntry(nil), t.logs...),
	}
	return s
}

func (t *DownloadTask) setState(state, message string) {
	t.mu.Lock()
	t.state = state
	if message != "" {
		t.message = message
	}
	t.mu.Unlock()
}

func (t *DownloadTask) finish(state, message string) {
	now := time.Now()
	t.mu.Lock()
	t.state = state
	t.message = message
	t.ended = &now
	t.mu.Unlock()
}

type Manager struct {
	mu         sync.RWMutex
	current    *DownloadTask
	defaultOut string
	provider   lines.Provider
	newClient  ClientFactory
}

func NewManager(provider lines.Provider, defaultOut string, factory ClientFactory) *Manager {
	return &Manager{defaultOut: defaultOut, provider: provider, newClient: factory}
}

func (m *Manager) DefaultOutput() string { return m.defaultOut }

func (m *Manager) OutputDir() string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if m.current != nil {
		return m.current.output
	}
	return m.defaultOut
}

func (m *Manager) Current() *DownloadTask {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.current
}

func (m *Manager) Snapshot() *TaskSnapshot {
	task := m.Current()
	if task == nil {
		return nil
	}
	snapshot := task.snapshot()
	return &snapshot
}

func activeState(state string) bool {
	return state == "preparing" || state == "running" || state == "cancelling"
}

func (m *Manager) Start(options DownloadOptions) (TaskSnapshot, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.current != nil && activeState(m.current.snapshot().State) {
		return TaskSnapshot{}, errors.New("已有下载任务正在运行，请先取消或等待完成")
	}
	if options.OutputDir == "" {
		options.OutputDir = m.defaultOut
	}
	if options.Category == 0 && options.Count == 0 {
		options.Category = 9
	}
	if options.Count == 0 {
		options.Count = 800
	}
	if options.Workers == 0 {
		options.Workers = 12
	}
	if options.Timeout == 0 {
		options.Timeout = 8
	}
	if options.Category < 0 || options.Category > 12 || options.Count < 1 || options.Count > 800 || options.Start < 0 {
		return TaskSnapshot{}, errors.New("周期、起始位置或条数无效；单只股票每次最多下载 800 条")
	}
	if options.Workers < 1 || options.Workers > 64 || options.Timeout < 2 || options.Timeout > 60 {
		return TaskSnapshot{}, errors.New("并发数须为 1–64，超时须为 2–60 秒")
	}
	if len(options.Markets) == 0 && strings.TrimSpace(options.Codes) == "" {
		return TaskSnapshot{}, errors.New("请至少选择一个市场或填写证券代码")
	}
	seenMarket := make(map[uint8]bool)
	for _, market := range options.Markets {
		if market > 2 || seenMarket[market] {
			return TaskSnapshot{}, errors.New("市场选择无效")
		}
		seenMarket[market] = true
	}
	if strings.TrimSpace(options.Codes) != "" {
		if _, err := parseCodes(options.Codes); err != nil {
			return TaskSnapshot{}, err
		}
	}
	output, err := filepath.Abs(options.OutputDir)
	if err != nil {
		return TaskSnapshot{}, fmt.Errorf("输出目录无效：%w", err)
	}
	if err := os.MkdirAll(output, 0o755); err != nil {
		return TaskSnapshot{}, fmt.Errorf("创建输出目录：%w", err)
	}
	options.OutputDir = output
	ctx, cancel := context.WithCancel(context.Background())
	task := &DownloadTask{
		id: time.Now().Format("20060102-150405"), state: "preparing", message: "正在生成配置并自检内置线路",
		output: output, options: options, provider: m.provider, newClient: m.newClient,
		started: time.Now(), ctx: ctx, cancel: cancel,
	}
	m.current = task
	task.log("info", "使用内置站点模板，无需 connect.cfg；并发 worker 数 %d", options.Workers)
	go task.run()
	return task.snapshot(), nil
}

func (m *Manager) Cancel() error {
	task := m.Current()
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

func (t *DownloadTask) run() {
	defer t.cancel()
	if t.provider == nil || t.newClient == nil {
		t.finish("failed", "下载服务未正确初始化")
		return
	}
	hosts, report, err := t.provider.Available(t.ctx, time.Duration(t.options.Timeout)*time.Second)
	if err != nil {
		if t.ctx.Err() != nil {
			t.finish("cancelled", "任务已取消")
		} else {
			t.finish("failed", "内置线路自检失败："+err.Error())
		}
		t.log("error", "线路不可用：%v", err)
		return
	}
	t.hosts = hosts
	t.setState("preparing", "线路业务自检通过，正在准备证券清单")
	t.log("info", "内置配置已生成并回读校验（%d 字节），业务自检通过 %d/%d 条线路", report.ConfigBytes, report.Available, report.Tested)
	if t.options.GBBQPath != "" {
		factors, err := adjust.LoadGBBQ(t.options.GBBQPath)
		if err != nil {
			t.log("warn", "复权因子载入失败，将继续保存原始行情：%v", err)
		} else {
			t.factors = factors
			t.log("info", "复权因子已载入，覆盖 %d 只证券", len(factors))
		}
	} else {
		t.log("info", "未配置 gbbq，本次仅保存原始行情")
	}
	var securities []Security
	if strings.TrimSpace(t.options.Codes) != "" {
		securities, _ = parseCodes(t.options.Codes)
		t.log("info", "使用手动证券清单，共 %d 只", len(securities))
	} else {
		for _, market := range t.options.Markets {
			if t.ctx.Err() != nil {
				t.finish("cancelled", "任务已取消")
				return
			}
			t.log("info", "正在读取 %s 市场码表", strings.ToUpper(marketPrefix(market)))
			list, err := t.discoverMarket(market)
			if err != nil {
				t.log("error", "%s 市场码表读取失败：%v", strings.ToUpper(marketPrefix(market)), err)
				continue
			}
			t.log("info", "%s 市场载入 %d 只证券", strings.ToUpper(marketPrefix(market)), len(list))
			securities = append(securities, list...)
		}
	}
	if len(securities) == 0 {
		if t.ctx.Err() != nil {
			t.finish("cancelled", "任务已取消")
		} else {
			t.finish("failed", "没有获取到可下载的证券")
		}
		return
	}
	t.mu.Lock()
	t.total = len(securities)
	t.state = "running"
	t.message = "正在下载并写入 CSV"
	t.mu.Unlock()

	jobs := make(chan Security, t.options.Workers*2)
	var wg sync.WaitGroup
	for i := 0; i < t.options.Workers; i++ {
		wg.Add(1)
		go func(offset int) {
			defer wg.Done()
			t.worker(offset, jobs)
		}(i % len(t.hosts))
	}
produce:
	for _, security := range securities {
		select {
		case jobs <- security:
		case <-t.ctx.Done():
			break produce
		}
	}
	close(jobs)
	wg.Wait()

	s := t.snapshot()
	if t.ctx.Err() != nil || s.State == "cancelling" {
		t.finish("cancelled", fmt.Sprintf("已取消；保留 %d 个完整文件", s.Succeeded))
	} else if s.Failed > 0 {
		t.finish("completed_with_errors", fmt.Sprintf("下载完成，%d 只失败", s.Failed))
	} else {
		t.finish("completed", "全部行情下载完成")
	}
	t.log("info", "任务结束：成功 %d，失败 %d，K 线 %d 条", s.Succeeded, s.Failed, s.Bars)
}

func (t *DownloadTask) discoverMarket(market uint8) ([]Security, error) {
	client := t.newClient(t.hosts, time.Duration(t.options.Timeout)*time.Second, int(market)%len(t.hosts))
	defer client.Close()
	count, err := client.SecurityCount(market)
	if err != nil {
		return nil, err
	}
	if count == 0 {
		return nil, nil
	}
	out := make([]Security, 0, count)
	for start := 0; start < count; start += 1000 {
		if t.ctx.Err() != nil {
			return nil, t.ctx.Err()
		}
		list, err := client.SecurityList(market, start, 1000)
		if err != nil {
			return nil, err
		}
		out = append(out, list...)
		if len(list) == 0 {
			break
		}
	}
	return out, nil
}

func (t *DownloadTask) worker(offset int, jobs <-chan Security) {
	client := t.newClient(t.hosts, time.Duration(t.options.Timeout)*time.Second, offset)
	defer client.Close()
	for {
		select {
		case <-t.ctx.Done():
			return
		case security, ok := <-jobs:
			if !ok || t.ctx.Err() != nil {
				return
			}
			started := time.Now()
			bars, err := client.SecurityBars(t.options.Category, security.Market, security.Code, t.options.Start, t.options.Count)
			if err == nil {
				err = t.writeCSV(security, bars)
			}
			if err == nil {
				events := t.factors[adjust.FactorKey(security.Market, security.Code)]
				if factorErr := adjust.WriteAdjustmentCSV(t.output, security, events); factorErr != nil {
					t.log("warn", "%s%s 复权因子保存失败：%v", marketPrefix(security.Market), security.Code, factorErr)
				}
			}
			duration := time.Since(started)
			t.mu.Lock()
			t.done++
			if err != nil {
				t.failed++
				t.mu.Unlock()
				t.log("error", "%s%s 下载失败：%v", marketPrefix(security.Market), security.Code, err)
				continue
			}
			t.success++
			t.bars += int64(len(bars))
			name := csvFileName(security, t.options.Category)
			path := filepath.Join(t.output, name)
			size := int64(0)
			if info, statErr := os.Stat(path); statErr == nil {
				size = info.Size()
			}
			t.recent = append([]FileResult{{Name: name, Code: security.Code, Market: strings.ToUpper(marketPrefix(security.Market)), Bars: len(bars), Size: size, Duration: duration.Milliseconds(), CreatedAt: time.Now().Format("15:04:05")}}, t.recent...)
			if len(t.recent) > 10 {
				t.recent = t.recent[:10]
			}
			t.mu.Unlock()
		}
	}
}

func (t *DownloadTask) writeCSV(security Security, bars []Bar) error {
	name := csvFileName(security, t.options.Category)
	finalPath := filepath.Join(t.output, name)
	file, err := os.CreateTemp(t.output, "."+name+"-*.tmp")
	if err != nil {
		return err
	}
	tempPath := file.Name()
	ok := false
	defer func() {
		_ = file.Close()
		if !ok {
			_ = os.Remove(tempPath)
		}
	}()
	writer := csv.NewWriter(file)
	if err = writer.Write([]string{"date", "open", "high", "low", "close", "volume", "amount"}); err == nil {
		for _, bar := range bars {
			layout := "2006-01-02"
			if t.options.Category < 4 || t.options.Category == 7 || t.options.Category == 8 {
				layout = "2006-01-02 15:04"
			}
			record := []string{
				bar.Date.Format(layout),
				strconv.FormatFloat(bar.Open, 'f', 3, 64), strconv.FormatFloat(bar.High, 'f', 3, 64),
				strconv.FormatFloat(bar.Low, 'f', 3, 64), strconv.FormatFloat(bar.Close, 'f', 3, 64),
				strconv.FormatFloat(bar.Volume, 'f', 0, 64), strconv.FormatFloat(bar.Amount, 'f', 2, 64),
			}
			if err = writer.Write(record); err != nil {
				break
			}
		}
	}
	writer.Flush()
	if err == nil {
		err = writer.Error()
	}
	if closeErr := file.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	if err = os.Rename(tempPath, finalPath); err != nil {
		return err
	}
	ok = true
	return nil
}

func csvFileName(security Security, category int) string {
	return fmt.Sprintf("%s%s_%s.csv", marketPrefix(security.Market), security.Code, categoryName(category))
}

func parseCodes(value string) ([]Security, error) {
	items := strings.FieldsFunc(value, func(r rune) bool {
		return r == ',' || r == ';' || r == '\n' || r == '\r' || r == '\t' || r == ' '
	})
	seen := make(map[string]bool)
	out := make([]Security, 0, len(items))
	for _, item := range items {
		item = strings.ToLower(strings.TrimSpace(item))
		market := uint8(255)
		for _, candidate := range []struct {
			market uint8
			prefix string
		}{{0, "sz"}, {1, "sh"}, {2, "bj"}} {
			if strings.HasPrefix(item, candidate.prefix) {
				market = candidate.market
				item = strings.TrimPrefix(item, candidate.prefix)
				break
			}
		}
		if len(item) != 6 {
			return nil, fmt.Errorf("证券代码 %q 无效，应为 6 位数字", item)
		}
		if _, err := strconv.Atoi(item); err != nil {
			return nil, fmt.Errorf("证券代码 %q 无效，应为 6 位数字", item)
		}
		if market == 255 {
			switch item[0] {
			case '5', '6', '9':
				market = 1
			case '4', '8':
				market = 2
			default:
				market = 0
			}
		}
		key := fmt.Sprintf("%d:%s", market, item)
		if !seen[key] {
			seen[key] = true
			out = append(out, Security{Market: market, Code: item})
		}
	}
	if len(out) == 0 {
		return nil, errors.New("请至少填写一个证券代码")
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Market == out[j].Market {
			return out[i].Code < out[j].Code
		}
		return out[i].Market < out[j].Market
	})
	return out, nil
}
