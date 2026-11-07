package service

import (
	"context"
	"sort"
	"sync"
	"sync/atomic"
	"time"
)

// rankedHost 保存一次真实业务请求的端到端耗时；失败线路仅降级，不从后备列表删除。
type rankedHost struct {
	host     Host
	duration time.Duration
	ok       bool
}

// rankHosts 用同一只证券的同一页行情测量线路，返回初始 worker 应使用的线路数。
// 大任务多一次逐线测速即可避免整个任务被慢线路拖住；小任务不增加任何额外请求。
func (t *jobRun) rankHosts(security Security) int {
	fallback := min(8, len(t.hosts))
	if len(t.hosts) <= 2 {
		return fallback
	}
	// 整个测速阶段最多两秒；凑齐八条成功线路就取消其余测速，不等无效线路超时。
	rankCtx, cancel := context.WithTimeout(t.ctx, 2*time.Second)
	defer cancel()
	timeout := min(time.Duration(t.options.Timeout)*time.Second, 3*time.Second)
	results := make([]rankedHost, len(t.hosts))
	var successful atomic.Int32
	var wg sync.WaitGroup
	for i, host := range t.hosts {
		wg.Add(1)
		go func() {
			defer wg.Done()
			client := t.newClient([]Host{host}, timeout, 0)
			if contextual, ok := client.(interface{ SetContext(context.Context) }); ok {
				contextual.SetContext(rankCtx)
			}
			defer client.Close()
			started := time.Now()
			_, err := client.SecurityBars(t.options.Category, security.Market, security.Code, 0, 800)
			results[i] = rankedHost{host: host, duration: time.Since(started), ok: err == nil}
			if err == nil {
				t.mu.Lock()
				t.pages++
				t.mu.Unlock()
				if successful.Add(1) == int32(min(8, len(t.hosts))) {
					cancel()
				}
			}
		}()
	}
	wg.Wait()
	sort.SliceStable(results, func(i, j int) bool {
		if results[i].ok != results[j].ok {
			return results[i].ok
		}
		return results[i].ok && results[i].duration < results[j].duration
	})

	available := 0
	for i, result := range results {
		t.hosts[i] = result.host
		if result.ok {
			available++
		}
	}
	if available == 0 {
		t.log("warn", "线路行情测速均未成功，将按原线路列表重试")
		return fallback
	}
	preferred := min(8, available)
	t.log("info", "真实行情测速完成：%d/%d 条可用，首选 %s（%d ms）", available, len(results), results[0].host.Address(), results[0].duration.Milliseconds())
	if preferred > 1 {
		t.log("info", "次选线路 %s（%d ms）", results[1].host.Address(), results[1].duration.Milliseconds())
	}
	return preferred
}
