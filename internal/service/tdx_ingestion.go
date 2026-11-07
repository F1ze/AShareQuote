package service

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"quotes/internal/domain"
)

// run 先检查存储和完整断点，再选择本地、通用历史源或通达信并发流程。
// 将完整恢复放在网络检测之前，保证已有归档在离线时仍能重建数据库。
func (t *jobRun) run() {
	defer t.cancel()
	// 第 1 步：先检查所选仓库能否工作；环境缺失或无法打开数据库时，不浪费时间下载整批行情。
	if checker, ok := t.warehouse.(interface{ Check(context.Context) error }); ok {
		if err := checker.Check(t.ctx); err != nil {
			t.finish("failed", err.Error())
			return
		}
	}
	// 第 2 步：全部断点有效则直接离线重放；返回 true 表示该路径已处理终态，应立即结束。
	if t.restoreEntirePlan() {
		return
	}
	// 第 3 步：本地文件和通用历史源各自接管完整执行流程，下面剩余部分仅负责通达信。
	if t.options.ImportDir != "" {
		t.runLocal()
		return
	}
	if t.historySource != nil {
		t.runHistorySource()
		return
	}
	if t.configureTraffic != nil {
		t.configureTraffic(t.options.RequestRate, t.options.HostRequestRate)
	}
	if t.provider == nil || t.newClient == nil {
		t.finish("failed", "下载服务未正确初始化")
		return
	}
	// 第 4 步：生成并回读内置配置，探测 TCP 连通线路，再载入可选 gbbq 事件。
	hosts, report, err := t.provider.Available(t.ctx, time.Duration(t.options.Timeout)*time.Second)
	// 无论探测成败，都向任务快照发布本次报告；不额外发起一轮探测来填充界面。
	t.mu.Lock()
	t.lineReport = &report
	t.mu.Unlock()
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
	if t.ctx.Err() != nil {
		t.finish("cancelled", "任务已取消")
		return
	}
	if len(hosts) == 0 {
		t.finish("failed", "没有可用行情线路，请检查网络后重试")
		return
	}
	t.setState("preparing", "线路 TCP 连通性检测通过，正在准备证券清单")
	t.log("info", "内置配置已生成并回读校验（%d 字节），TCP 检测通过 %d/%d 条线路", report.ConfigBytes, report.Available, report.Tested)
	if t.options.GBBQPath != "" {
		if t.factorRepository == nil {
			t.finish("failed", "未配置因子仓库")
			return
		}
		factors, err := t.factorRepository.LoadGBBQ(t.ctx, t.options.GBBQPath)
		if err != nil {
			t.finish("failed", "指定的复权因子读取失败："+err.Error())
			return
		} else {
			t.factors = factors
			t.log("info", "复权因子已载入，覆盖 %d 只证券", len(factors))
		}
	} else {
		t.log("info", "未配置 gbbq，本次仅保存原始行情")
	}
	// 第 5 步：确定清单（原计划 > 手填代码 > 市场码表），再做 A 股筛选和可选名录观察入库。
	// 当前码表仅用于新任务，不能用它证明历史证券全体已覆盖；任一市场清单失败则整项停止。
	var securities []Security
	if t.resumePlan != nil && len(t.resumePlan.Securities) > 0 {
		securities = t.resumePlan.Securities
	} else if strings.TrimSpace(t.options.Codes) != "" {
		securities, _ = domain.ParseCodes(t.options.Codes)
		t.log("info", "使用手动证券清单，共 %d 只", len(securities))
	} else {
		for _, marketValue := range t.options.Markets {
			market := uint8(marketValue)
			if t.ctx.Err() != nil {
				t.finish("cancelled", "任务已取消")
				return
			}
			t.log("info", "正在读取 %s 市场码表", strings.ToUpper(marketPrefix(market)))
			list, err := t.discoverMarket(market)
			if err != nil {
				t.log("error", "%s 市场码表读取失败：%v", strings.ToUpper(marketPrefix(market)), err)
				if t.ctx.Err() != nil {
					t.finish("cancelled", "任务已取消")
				} else {
					t.finish("failed", "证券清单读取不完整，请重试；未开始写入行情")
				}
				return
			}
			t.log("info", "%s 市场载入 %d 只证券", strings.ToUpper(marketPrefix(market)), len(list))
			securities = append(securities, list...)
		}
	}
	if t.options.ASharesOnly {
		filtered := make([]Security, 0, len(securities))
		for _, s := range securities {
			if domain.IsAShare(s) {
				filtered = append(filtered, s)
			}
		}
		securities = filtered
		t.log("info", "A 股范围筛选后共 %d 只；证券名单为当前行情源观察值，不代表历史全部上市/退市证券", len(securities))
	}
	if len(securities) == 0 {
		if t.ctx.Err() != nil {
			t.finish("cancelled", "任务已取消")
		} else {
			t.finish("failed", "没有获取到可下载的证券")
		}
		return
	}
	if observer, ok := t.warehouse.(interface {
		Observe(context.Context, []domain.Security) error
	}); ok {
		if err := observer.Observe(t.ctx, securities); err != nil {
			t.finish("failed", "证券目录保存失败："+err.Error())
			return
		}
	}
	// 第 6 步：固定总量并持久化 planned 事件，计划落盘后才允许 worker 写行情。
	t.mu.Lock()
	t.total = len(securities)
	t.state = "running"
	t.message = "正在下载并写入 CSV"
	t.mu.Unlock()
	if t.journal != nil {
		s := t.snapshot()
		if err := t.journal.Append(t.id, runEvent{Kind: "planned", Snapshot: &s, Plan: &ingestionPlan{Securities: securities}}); err != nil {
			t.finish("failed", "保存任务计划失败："+err.Error())
			return
		}
	}

	// 第 7 步：启动 worker、按需投递证券、关队列并等待收尾。
	// 有界队列提供背压，避免一次塞入全部任务；每个 worker 从不同线路偏移开始连接。
	workerCount := min(t.options.Workers, len(securities))
	t.log("info", "并发上限 %d，出站启动频率全局 %d 次/秒、单站 %d 次/秒（连接、握手、重试均计入）", workerCount, t.options.RequestRate, t.options.HostRequestRate)
	jobs := make(chan Security, workerCount*2)
	var wg sync.WaitGroup
	for i := 0; i < workerCount; i++ {
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
	// 仅生产者关闭队列；等 worker 全部退出后再写终态，避免“任务结束”后仍有写文件动作。
	close(jobs)
	wg.Wait()

	// 第 8 步：按取消、全失败、部分错误/覆盖提示、完全成功的优先级归纳终态并记录。
	// completed 仅说明本次处理无错误，不提供全市场/全历史完整性的证明。
	s := t.snapshot()
	if t.ctx.Err() != nil || s.State == "cancelling" {
		t.finish("cancelled", fmt.Sprintf("已取消；保留 %d 个完整文件", s.Succeeded))
	} else if s.Succeeded == 0 && s.Failed > 0 {
		t.finish("failed", "本次没有成功保存的证券，请检查运行记录")
	} else if s.Failed > 0 || s.Warnings > 0 {
		t.finish("completed_with_errors", fmt.Sprintf("下载结束：%d 只失败，%d 个文件需检查日期覆盖", s.Failed, s.Warnings))
	} else {
		t.finish("completed", "全部行情下载完成")
	}
	t.log("info", "任务结束：成功 %d，失败 %d，K 线 %d 条", s.Succeeded, s.Failed, s.Bars)
}

// discoverMarket 要么返回完整码表，要么失败；提前空页或重复页不能当作成功结束。
// 否则全市场任务会静默遗漏证券，却仍向用户报告完成。
func (t *jobRun) discoverMarket(market uint8) ([]Security, error) {
	client := t.newClient(t.hosts, time.Duration(t.options.Timeout)*time.Second, int(market)%len(t.hosts))
	if contextual, ok := client.(interface{ SetContext(context.Context) }); ok {
		contextual.SetContext(t.ctx)
	}
	defer client.Close()
	count, err := client.SecurityCount(market)
	if err != nil {
		return nil, err
	}
	if count == 0 {
		return nil, nil
	}
	out := make([]Security, 0, count)
	seen := make(map[string]bool)
	for start := 0; start < count; {
		if t.ctx.Err() != nil {
			return nil, t.ctx.Err()
		}
		list, err := client.SecurityList(market, start, 1000)
		if err != nil {
			return nil, err
		}
		if len(list) == 0 {
			return nil, fmt.Errorf("证券清单在 %d/%d 处提前结束", start, count)
		}
		added := 0
		for _, security := range list {
			if !seen[security.Code] {
				seen[security.Code] = true
				out = append(out, security)
				added++
			}
		}
		if added == 0 {
			return nil, errors.New("行情源重复返回证券清单，请重试")
		}
		start += len(list)
	}
	return out, nil
}
