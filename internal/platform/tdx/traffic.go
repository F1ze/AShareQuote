package tdx

import (
	"context"
	"sync"
	"time"

	"quotes/internal/domain"
)

// TrafficController 为同一应用内的客户端共享发包预算，而不是给每个 worker 一份独立额度。
// 按请求启动频率平滑限流，不允许突发堆积；它不限制响应字节数，也不保证服务端不会拒绝请求。
type TrafficController struct {
	mu         sync.Mutex           // 原子检查并消耗全局/单站两个预算，等待时不持锁。
	globalGap  time.Duration        // 相邻全局许可的最小间隔。
	hostGap    time.Duration        // 同一 IP/端口相邻许可的最小间隔。
	nextGlobal time.Time            // 最早能发放下一个全局许可的时间。
	nextByHost map[string]time.Time // 每个站点独立的下一许可时间；共享同一连接与不同连接均合并计数。
}

func NewTrafficController(global, perHost int) *TrafficController {
	t := &TrafficController{nextByHost: make(map[string]time.Time)}
	t.Configure(global, perHost)
	return t
}

// Configure 在任务探测之前更新策略；保留已经消耗的时间预算，避免新任务/新连接重置额度造成突发。
func (t *TrafficController) Configure(global, perHost int) {
	if global <= 0 {
		global = domain.DefaultRequestRate
	}
	if perHost <= 0 {
		perHost = domain.DefaultHostRequestRate
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	t.globalGap = time.Second / time.Duration(global)
	t.hostGap = time.Second / time.Duration(perHost)
}

// Wait 的流程：检查取消 → 同时检查两个预算 → 有额度则消费，否则可取消地等待后重新竞争。
// 等待者不预占未来额度，取消不会留下空洞；单站等待也不会占住全局预算，其他站点仍可工作。
func (t *TrafficController) Wait(ctx context.Context, host Host) error {
	address := host.Address()
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		t.mu.Lock()
		now := time.Now()
		next := t.nextGlobal
		if t.nextByHost[address].After(next) {
			next = t.nextByHost[address]
		}
		if !next.After(now) {
			t.nextGlobal = now.Add(t.globalGap)
			t.nextByHost[address] = now.Add(t.hostGap)
			t.mu.Unlock()
			return nil
		}
		t.mu.Unlock()
		timer := time.NewTimer(time.Until(next))
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
	}
}
