package service

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"quotes/internal/domain"
)

// RealtimeSource 复用通达信参考模块的批量快照和有限成交窗口；每次请求独占协议客户端。
type RealtimeSource interface {
	RealtimeQuotes([]domain.Security) ([]domain.RealtimeQuote, error)
	RealtimeTrades(domain.Security) ([]domain.RealtimeTrade, error)
}

// RealtimeNames 从已有名录补充名称，缺失不阻塞行情；不能从股票代码臆造名称。
type RealtimeNames interface {
	SecurityNames(context.Context, []domain.Security) (map[string]string, error)
}

// RealtimeNameFallback 返回当前缓存的名称，缺失时可异步补齐。
type RealtimeNameFallback interface {
	Names([]domain.Security) map[string]string
}

type realtimeNameLookup struct {
	local    RealtimeNames        // 优先读取本地证券参考信息。
	fallback RealtimeNameFallback // 仅补充本地缺失的名称。
}

func WithRealtimeNameFallback(local RealtimeNames, fallback RealtimeNameFallback) RealtimeNames {
	return realtimeNameLookup{local: local, fallback: fallback}
}

func (n realtimeNameLookup) SecurityNames(ctx context.Context, stocks []domain.Security) (map[string]string, error) {
	names, err := n.local.SecurityNames(ctx, stocks)
	if err != nil || names == nil {
		names = make(map[string]string)
	}
	var missing []domain.Security
	for _, stock := range stocks {
		if strings.TrimSpace(names[domain.MarketPrefix(stock.Market)+stock.Code]) == "" {
			missing = append(missing, stock)
		}
	}
	if len(missing) > 0 && ctx.Err() == nil {
		for key, name := range n.fallback.Names(missing) {
			names[key] = name
		}
	}
	return names, nil
}

type realtimeEntry struct {
	snapshot domain.RealtimeSnapshot // 构建后只读，返回 JSON 时不再修改其切片。
	at       time.Time               // 缓存完成时间；不使用它替换源端实际接收时间。
}

type RealtimeService struct {
	watch     domain.RealtimeWatch                 // 最近由主页面发布的自选，仅在进程内保存；复用 mu 保护。
	newSource func(context.Context) RealtimeSource // 由组装根注入拨号、限流与站点。
	names     RealtimeNames                        // 可选的本地名录查询端口。
	gate      chan struct{}                        // 只允许一轮快照刷新在上游执行，避免多个浏览器放大请求。
	mu        sync.Mutex                           // 保护短缓存；网络读写期间不持锁。
	cache     map[string]realtimeEntry             // 最多 32 组自选，超过后淘汰最早完成项。
}

func NewRealtimeService(factory func(context.Context) RealtimeSource, names RealtimeNames) *RealtimeService {
	return &RealtimeService{newSource: factory, names: names, gate: make(chan struct{}, 1), cache: map[string]realtimeEntry{}}
}

// ParseRealtimeSelection 统一规范键并去重；限制 20 只，防止把看盘接口当全市场高频扫描器。
func ParseRealtimeSelection(codes, selected string) ([]domain.Security, domain.Security, error) {
	if len(codes) > 512 || len(selected) > 32 {
		return nil, domain.Security{}, fmt.Errorf("实时证券参数过长")
	}
	stocks, err := domain.ParseCodes(codes)
	if err != nil || len(stocks) == 0 || len(stocks) > 20 {
		return nil, domain.Security{}, fmt.Errorf("请填写 1–20 只有效证券代码")
	}
	current, err := domain.ParseCodes(selected)
	if err != nil || len(current) != 1 {
		return nil, domain.Security{}, fmt.Errorf("请选择一只主屏股票")
	}
	found := false
	for _, stock := range stocks {
		if !domain.IsTDXInstrument(stock) {
			return nil, domain.Security{}, fmt.Errorf("%s 不属于支持的 A 股、ETF 或可转债号段", stock.Code)
		}
		if stock.Market == current[0].Market && stock.Code == current[0].Code {
			found = true
		}
	}
	if !found {
		return nil, domain.Security{}, fmt.Errorf("主屏股票须位于监控列表中")
	}
	return stocks, current[0], nil
}

// Snapshot 沿用 tdx_dash 的短缓存，同时将快照与明细错误隔离，明细不可用不掩盖五档行情。
// 没有授权和十档协议证据，始终保留 L2Verified=false；非空明细不等同完整 L2 权限。
func (s *RealtimeService) Snapshot(ctx context.Context, stocks []domain.Security, selected domain.Security) (domain.RealtimeSnapshot, error) {
	return s.snapshot(ctx, stocks, selected, true)
}

// Quotes 供轻量小组件使用，只读取一批快照，不发起扩展成交请求。
func (s *RealtimeService) Quotes(ctx context.Context, stocks []domain.Security, selected domain.Security) (domain.RealtimeSnapshot, error) {
	return s.snapshot(ctx, stocks, selected, false)
}

func (s *RealtimeService) snapshot(ctx context.Context, stocks []domain.Security, selected domain.Security, withDetails bool) (domain.RealtimeSnapshot, error) {
	if s == nil || s.newSource == nil {
		return domain.RealtimeSnapshot{}, fmt.Errorf("实时行情未装配")
	}
	// 请求可来自其他消费端，仍在用例层执行数量、市场和选中项约束。
	keys := make([]string, len(stocks))
	for i, stock := range stocks {
		keys[i] = domain.MarketPrefix(stock.Market) + stock.Code
	}
	selectedKey := domain.MarketPrefix(selected.Market) + selected.Code
	stocks, selected, err := ParseRealtimeSelection(strings.Join(keys, ","), selectedKey)
	if err != nil {
		return domain.RealtimeSnapshot{}, err
	}
	if err := ctx.Err(); err != nil {
		return domain.RealtimeSnapshot{}, err
	}
	sort.Strings(keys)
	key := strings.Join(keys, ",") + "/" + selectedKey
	if !withDetails {
		key += "/quotes"
	}
	select {
	case s.gate <- struct{}{}:
		defer func() { <-s.gate }()
	case <-ctx.Done():
		return domain.RealtimeSnapshot{}, ctx.Err()
	}
	if err := ctx.Err(); err != nil {
		return domain.RealtimeSnapshot{}, err
	}
	s.mu.Lock()
	hit, ok := s.cache[key]
	s.mu.Unlock()
	if ok && time.Since(hit.at) < 900*time.Millisecond {
		result := hit.snapshot
		result.Cached = true
		return result, nil
	}
	result := domain.RealtimeSnapshot{Selected: selectedKey, Quotes: []domain.RealtimeQuote{}, Trades: []domain.RealtimeTrade{}, DetailStatus: "unavailable", DetailMessage: "完整 L2 未接入：无十档盘口、独立逐笔委托和授权接口；当前仅尝试扩展成交明细"}
	// 两条独立短连接并行；明细期限更短，失败不会使整屏等待三轮协议超时。
	type detailResult struct {
		rows []domain.RealtimeTrade
		err  error
	}
	detail := make(chan detailResult, 1)
	detailCtx, cancelDetails := context.WithTimeout(ctx, 3*time.Second)
	defer cancelDetails()
	if withDetails {
		go func() { rows, e := s.newSource(detailCtx).RealtimeTrades(selected); detail <- detailResult{rows, e} }()
	}
	quoteCtx, cancelQuotes := context.WithTimeout(ctx, 6*time.Second)
	defer cancelQuotes()
	started := time.Now()
	quotes, err := s.newSource(quoteCtx).RealtimeQuotes(stocks)
	result.LatencyMS = time.Since(started).Milliseconds()
	result.ReceivedAt = time.Now().UTC()
	if err != nil {
		return result, err
	}
	if ctx.Err() != nil {
		return result, ctx.Err()
	}
	if s.names != nil {
		names, e := s.names.SecurityNames(ctx, stocks)
		if e == nil {
			result.Names = names
			for i := range quotes {
				quotes[i].Name = names[domain.MarketPrefix(quotes[i].Market)+quotes[i].Code]
			}
		}
	}
	result.Quotes = quotes
	if withDetails {
		select {
		case d := <-detail:
			if d.err != nil {
				result.DetailMessage = "扩展成交明细不可用（网络、站点能力或权限均可能导致），已降级五档快照：" + d.err.Error()
			} else if len(d.rows) == 0 {
				result.DetailStatus = "empty"
				result.DetailMessage = "本轮没有成交明细，可能休市或该站点无数据；不能据此判断 L2 权限"
			} else {
				result.DetailStatus = "available"
				result.Trades = d.rows
			}
		case <-detailCtx.Done():
			result.DetailMessage = "扩展成交明细超时，已降级五档快照；完整 L2 权限仍未验证"
		}
	} else {
		result.DetailStatus = "not_requested"
		result.DetailMessage = "轻量报价模式，未请求扩展成交"
	}
	if ctx.Err() != nil {
		return result, ctx.Err()
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.cache) >= 32 {
		var oldestKey string
		var at time.Time
		for k, v := range s.cache {
			if oldestKey == "" || v.at.Before(at) {
				oldestKey, at = k, v.at
			}
		}
		delete(s.cache, oldestKey)
	}
	s.cache[key] = realtimeEntry{snapshot: result, at: time.Now()}
	return result, nil
}

// Watch 返回配置副本，防止客户端或调用方修改服务内存中的订阅切片。
func (s *RealtimeService) Watch() domain.RealtimeWatch {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.watch.Codes) == 0 {
		return domain.RealtimeWatch{Codes: []string{"sh600036", "sh600519", "sz000001", "sz300750"}, Selected: "sh600036", Interval: 3}
	}
	result := s.watch
	result.Codes = append([]string(nil), s.watch.Codes...)
	return result
}

// SetWatch 校验主页面配置后发布给轻量客户端，不在此处发起行情请求。
func (s *RealtimeService) SetWatch(watch domain.RealtimeWatch) error {
	stocks, selected, err := ParseRealtimeSelection(strings.Join(watch.Codes, ","), watch.Selected)
	if err != nil {
		return err
	}
	if watch.Interval < 2 || watch.Interval > 30 {
		return fmt.Errorf("刷新间隔须为 2–30 秒")
	}
	valid := make(map[string]bool, len(stocks))
	for _, stock := range stocks {
		valid[domain.MarketPrefix(stock.Market)+stock.Code] = true
	}
	codes := make([]string, 0, len(stocks))
	for _, key := range watch.Codes {
		if valid[key] {
			codes = append(codes, key)
			delete(valid, key)
		}
	}
	watch.Codes = codes
	watch.Selected = domain.MarketPrefix(selected.Market) + selected.Code
	if len(codes) != len(stocks) {
		return fmt.Errorf("请使用 sh/sz/bj 开头的规范代码")
	}
	s.mu.Lock()
	s.watch = watch
	s.mu.Unlock()
	return nil
}
