package service

import (
	"context"
	"fmt"
	"time"

	"quotes/internal/domain"
)

// RegisterSource 在启动组装阶段注册历史来源及其能力，服务开始处理请求后不再修改注册表。
// 新来源实现端口即可接入，无需在任务调度中逐个判断供应商名称。
func (m *JobService) RegisterSource(spec SourceSpec) error {
	if spec.Key == "" || spec.Key == "tdx" || spec.Adapter == nil || len(spec.Markets) == 0 || len(spec.Categories) == 0 {
		return fmt.Errorf("来源注册信息无效")
	}
	if m.historySources == nil {
		m.historySources = make(map[string]SourceSpec)
	}
	if _, exists := m.historySources[spec.Key]; exists {
		return fmt.Errorf("来源 %s 已注册", spec.Key)
	}
	m.historySources[spec.Key] = spec
	return nil
}

// runHistorySource 基于历史名录顺序采集，复用统一归档、入库和成功断点流水线。
// 名录与日历用于补充研究参考，不能据此自动声称行情已经覆盖全市场完整历史。
func (t *jobRun) runHistorySource() {
	// 第 1 步：确定市场与原始名录。手填代码时先提取其市场，恢复时优先使用原任务的名录。
	t.setState("preparing", "正在读取包含退市证券的历史名录")
	// SDK 会话按顺序使用，不套用通达信的 TCP worker 数，避免会话串扰和放大公网请求压力。
	markets := t.options.Markets
	if t.options.Codes != "" {
		codes, _ := domain.ParseCodes(t.options.Codes)
		seen := make(map[uint8]bool)
		markets = nil
		for _, s := range codes {
			if !seen[s.Market] {
				seen[s.Market] = true
				markets = append(markets, int(s.Market))
			}
		}
	}
	var securities []Security
	var err error
	if t.resumePlan != nil && len(t.resumePlan.Securities) > 0 {
		securities = t.resumePlan.Securities
	} else {
		securities, err = t.historySource.Universe(t.ctx, markets)
	}
	if err != nil {
		t.finish("failed", "历史证券名录读取失败："+err.Error())
		return
	}
	// 第 2 步：按 A 股、手填清单和上市/退市区间筛选；保留与请求时段相交的退市证券。
	selected := make(map[string]bool)
	if t.options.Codes != "" {
		codes, _ := domain.ParseCodes(t.options.Codes)
		for _, s := range codes {
			selected[marketPrefix(s.Market)+s.Code] = true
		}
	}
	filtered := make([]Security, 0, len(securities))
	for _, s := range securities {
		if !domain.IsAShare(s) {
			continue
		}
		if len(selected) > 0 && !selected[marketPrefix(s.Market)+s.Code] {
			continue
		}
		// 仅排除生命周期与请求区间完全不相交的证券，不能按“当前是否上市”排除退市股。
		if s.ListingDate != "" && s.ListingDate > t.options.EndDate {
			continue
		}
		if s.DelistingDate != "" && s.DelistingDate < t.options.StartDate {
			continue
		}
		filtered = append(filtered, s)
	}
	securities = filtered
	t.log("info", "从历史名录中选择 %d 只 A 股（包含源提供的退市证券）", len(securities))
	if len(securities) == 0 {
		t.finish("failed", "该时段与证券范围内没有可用历史证券")
		return
	}
	// 第 3 步：仓库支持相应扩展能力时，先同步证券参考资料及交易日历。
	// 这些写入有各自事务；后面某只行情失败不会回滚本阶段已同步的参考资料。
	if observer, ok := t.warehouse.(interface {
		Observe(context.Context, []domain.Security) error
	}); ok {
		if err := observer.Observe(t.ctx, securities); err != nil {
			t.finish("failed", err.Error())
			return
		}
	}
	if sink, ok := t.warehouse.(interface {
		ObserveCalendar(context.Context, []domain.TradingDay) error
	}); ok {
		t.setState("preparing", "正在同步历史交易日历")
		calendar, err := t.historySource.Calendar(t.ctx, t.options.StartDate, t.options.EndDate)
		if err != nil {
			t.finish("failed", "交易日历读取失败："+err.Error())
			return
		}
		if err = sink.ObserveCalendar(t.ctx, calendar); err != nil {
			t.finish("failed", err.Error())
			return
		}
	}
	// 第 4 步：固定并持久化采集计划，保证重启后不会用变动的在线名录替换本次范围。
	t.mu.Lock()
	t.total = len(securities)
	t.state = "running"
	t.message = "顺序采集历史名录中的沪深 A 股（包含源提供的退市证券）"
	t.mu.Unlock()
	if t.journal != nil {
		s := t.snapshot()
		if err := t.journal.Append(t.id, runEvent{Kind: "planned", Snapshot: &s, Plan: &ingestionPlan{Securities: securities}}); err != nil {
			t.finish("failed", err.Error())
			return
		}
	}
	// 第 5 步：逐证券串行处理，单项错误交 recordOutcome 记录后继续下一只。
	for _, security := range securities {
		if t.ctx.Err() != nil {
			break
		}
		if t.restoreCheckpoint(security, t.options.Category) {
			continue
		}
		// 5.1 有效断点已在上面复用；其余项按生命周期和增量回看窗口计算实际请求范围。
		started := time.Now()
		// 在用户范围内按已知上市/退市日期裁剪，避免为生命周期之外的日期请求无效历史。
		from, to := t.options.StartDate, t.options.EndDate
		if security.ListingDate > from {
			from = security.ListingDate
		}
		if security.DelistingDate != "" && security.DelistingDate < to {
			to = security.DelistingDate
		}
		var err error
		if t.options.Incremental && t.archive != nil {
			latest, e := t.archive.Latest(t.ctx, security, t.options.Category)
			err = e
			if !latest.IsZero() {
				overlap := latest.AddDate(0, 0, -7).Format("2006-01-02")
				if overlap > from && overlap <= to {
					from = overlap
				}
			}
		}
		// 5.2 读取标准未复权行情。SDK 内部如何分页由适配器负责，本层不把调用次数当成实际页数。
		var bars []Bar
		if err == nil {
			bars, err = t.historySource.Bars(t.ctx, security, t.options.Category, from, to)
		}
		t.mu.Lock()
		t.pages++
		t.mu.Unlock()
		batch := domain.Batch{Security: security, Category: t.options.Category, Bars: bars, Source: t.options.Source, RequestedFrom: from, RequestedTo: to, RunID: t.id}
		var file FileResult
		var hash string
		// 5.3 因子是可选扩展能力，独立归档和入库；获取失败时本证券不能写成功断点。
		// 不直接复权原始行情，也不把供应商比例因子与通达信分红送配事件混用。
		// 因子从历史起点取到用户结束日，而非仅取增量窗口，避免刷新时丢掉较早事件。
		// 因子先于行情归档提交，后续行情保存失败也不回滚因子；恢复依靠幂等重放而非跨资源事务。
		if err == nil && t.options.CollectFactors {
			if t.factorRepository == nil {
				t.recordOutcome(batch, file, hash, 0, fmt.Errorf("未配置因子仓库"), false)
				continue
			}
			source, sourceOK := t.historySource.(interface {
				Factors(context.Context, domain.Security, string, string) ([]domain.PriceFactor, error)
			})
			sink, sinkOK := t.warehouse.(interface {
				StoreFactors(context.Context, domain.Security, []domain.PriceFactor) error
			})
			if !sourceOK {
				err = fmt.Errorf("该来源未提供复权因子接口")
			} else {
				var factors []domain.PriceFactor
				factors, err = source.Factors(t.ctx, security, "1990-12-19", t.options.EndDate)
				if err == nil {
					err = t.factorRepository.SaveProviderFactors(t.ctx, t.output, security, factors)
				}
				if err == nil && sinkOK {
					err = sink.StoreFactors(t.ctx, security, factors)
				}
			}
		}
		// 5.4 取得所需资源后才归档行情并入库；最后写断点，任何步骤错误都不能计作成功。
		if err == nil {
			file, hash, err = t.persistBatch(batch, started)
		}
		t.recordOutcome(batch, file, hash, len(bars), err, false)
	}
	// 第 6 步：所有证券尝试结束后归纳终态；来源可得记录已保存不等于历史完整已核验。
	s := t.snapshot()
	if t.ctx.Err() != nil {
		t.finish("cancelled", "历史采集已停止，完成断点已保存")
	} else if s.Succeeded == 0 {
		t.finish("failed", "本次没有成功入库的证券，请检查来源和运行记录")
	} else if s.Failed > 0 || s.Warnings > 0 {
		t.finish("completed_with_errors", fmt.Sprintf("历史采集结束，%d 只失败，%d 个覆盖提示", s.Failed, s.Warnings))
	} else {
		t.finish("completed", "历史证券行情入库完成；数据质量仍需结合研究参考信息核验")
	}
}
