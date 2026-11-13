package tdx

import (
	"context"
	"strings"
	"sync"
	"time"

	"quotes/internal/domain"
)

type nameCatalog struct {
	names   map[string]string // 已读取的证券名称，由目录锁保护。
	retryAt time.Time         // 刷新成功后缓存一天，失败时延迟重试。
	loading bool              // 同一市场只允许一次后台刷新。
}

// RealtimeNameCatalog 按需补充本地仓库缺少的证券名称，行情请求无需等待翻页。
type RealtimeNameCatalog struct {
	ctx       context.Context               // 应用退出时取消后台刷新。
	newClient func(context.Context) *Client // 使用组装根注入的线路、代理与共享限流。
	mu        sync.Mutex                    // 保护各市场的缓存和刷新状态。
	markets   [3]nameCatalog                // 按通达信市场编号隔离同码证券。
}

func NewRealtimeNameCatalog(ctx context.Context, factory func(context.Context) *Client) *RealtimeNameCatalog {
	return &RealtimeNameCatalog{ctx: ctx, newClient: factory}
}

// Names 返回当前缓存的名称，并为缓存过期或未加载的市场启动一次后台刷新。
func (c *RealtimeNameCatalog) Names(securities []domain.Security) map[string]string {
	c.mu.Lock()
	defer c.mu.Unlock()

	names := make(map[string]string, len(securities))
	now := time.Now()
	for _, security := range securities {
		market := security.Market
		if int(market) >= len(c.markets) {
			continue
		}
		entry := &c.markets[market]
		if name := entry.names[security.Code]; name != "" {
			names[domain.MarketPrefix(market)+security.Code] = name
		}
		if c.ctx.Err() == nil && !entry.loading && !now.Before(entry.retryAt) {
			entry.loading = true
			go c.refresh(market)
		}
	}
	return names
}

func (c *RealtimeNameCatalog) refresh(market uint8) {
	ctx, cancel := context.WithTimeout(c.ctx, 45*time.Second)
	defer cancel()
	client := c.newClient(ctx)
	defer client.Close()

	ok := readRealtimeNames(client, market, func(list []Security) {
		c.mu.Lock()
		defer c.mu.Unlock()
		entry := &c.markets[market]
		if entry.names == nil {
			entry.names = make(map[string]string)
		}
		for _, security := range list {
			if name := strings.TrimSpace(security.Name); name != "" {
				entry.names[security.Code] = name
			}
		}
	})
	c.mu.Lock()
	defer c.mu.Unlock()
	entry := &c.markets[market]
	entry.loading = false
	entry.retryAt = time.Now().Add(10 * time.Second)
	if ok {
		entry.retryAt = time.Now().Add(24 * time.Hour)
	}
}

func readRealtimeNames(client *Client, market uint8, publish func([]Security)) bool {
	if market == domain.MarketBJ {
		list, err := client.BeijingSecurities()
		publish(list)
		return err == nil && len(list) > 0
	}
	total, err := client.SecurityCount(market)
	if err != nil {
		return false
	}
	// 逐页发布，后续页面出错也保留已经读到的名称。
	seen := make(map[string]bool)
	for start := 0; start < total; {
		list, err := client.SecurityList(market, start, 1000)
		if err != nil || len(list) == 0 {
			return false
		}
		before := len(seen)
		for _, security := range list {
			seen[security.Code] = true
		}
		if len(seen) == before {
			return false
		}
		publish(list)
		start += len(list)
	}
	return len(seen) > 0
}
