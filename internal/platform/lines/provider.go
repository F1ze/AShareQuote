// Package lines 从内置站点配置生成并验证可用行情线路，端口可连接不等于业务可用。
package lines

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"quotes/internal/domain"
	"quotes/internal/platform/sitecfg"
)

// Provider 返回通过业务验证、按耗时排序的线路，任务层无需了解配置来源与探测实现。
type Provider interface {
	Available(ctx context.Context, timeout time.Duration) ([]domain.Host, Report, error)
}

type ProviderFunc func(context.Context, time.Duration) ([]domain.Host, Report, error)

func (f ProviderFunc) Available(ctx context.Context, timeout time.Duration) ([]domain.Host, Report, error) {
	return f(ctx, timeout)
}

type HostStatus = domain.HostStatus
type Report = domain.LineReport

// SmokeClient 是线路自检所需的最小协议接口，不包含完整的行情下载能力。
type SmokeClient interface {
	Close()
	SecurityCount(market uint8) (int, error)
	TransactionData(market uint8, code string, start, count int) ([]domain.Transaction, error)
}

type SmokeFactory func(host domain.Host, timeout time.Duration) SmokeClient

type BuiltinProvider struct {
	spec    func() sitecfg.Spec // 每次探测生成新的配置输入，避免在多次自检间共享可变配置。
	factory SmokeFactory        // 为每条待探测线路构造独占客户端，完成后由探测协程关闭。
}

func NewBuiltin(factory SmokeFactory) *BuiltinProvider {
	return NewBuiltinWithSpec(sitecfg.SeedSpec, factory)
}

func NewBuiltinWithSpec(spec func() sitecfg.Spec, factory SmokeFactory) *BuiltinProvider {
	return &BuiltinProvider{spec: spec, factory: factory}
}

// Available 依次完成默认配置生成、回读校验和真实业务探测；connect.cfg 仅在内存中流转。
// 只有同时取到上海码表和样本逐笔记录才算可用，不能仅以 TCP 建连成功为依据。
func (p *BuiltinProvider) Available(ctx context.Context, timeout time.Duration) ([]domain.Host, Report, error) {
	// 第 1 步：构建并回读配置，提取 HQHOST/TRYHQHOST 去重线路，最多安排前 24 条自检。
	report := Report{Source: "builtin"}
	if p == nil || p.spec == nil || p.factory == nil {
		return nil, report, fmt.Errorf("内置线路提供器未正确初始化")
	}
	raw, config, err := sitecfg.Build(p.spec())
	if err != nil {
		return nil, report, err
	}
	report.ConfigChecked = true
	report.ConfigBytes = len(raw)
	report.ConfigWarnings = len(config.Warnings)
	hosts := quoteHosts(config)
	report.Total = len(hosts)
	if len(hosts) == 0 {
		return nil, report, fmt.Errorf("内置配置没有行情线路")
	}
	if len(hosts) > 24 {
		hosts = hosts[:24]
	}
	report.Tested = len(hosts)
	// 第 2 步：有界并发执行业务探测，probe 等待全部结果并按可用性、总耗时排序。
	report.Results = probe(ctx, hosts, timeout, p.factory)
	byAddress := make(map[string]domain.Host, len(hosts))
	for _, host := range hosts {
		byAddress[host.Address()] = host
	}
	// 第 3 步：只将通过业务校验的 Host 返回给任务；全部失败时给出有限个诊断，不能继续盲下。
	ordered := make([]domain.Host, 0, len(hosts))
	for _, status := range report.Results {
		if status.OK {
			report.Available++
			ordered = append(ordered, byAddress[status.Address])
		}
	}
	if len(ordered) == 0 {
		details := make([]string, 0, 3)
		for _, status := range report.Results {
			if status.Error != "" {
				details = append(details, status.Address+"："+status.Error)
			}
			if len(details) == 3 {
				break
			}
		}
		return nil, report, fmt.Errorf("全部内置线路业务自检失败（码表 + 600000 逐笔）：%s", strings.Join(details, "；"))
	}
	return ordered, report, nil
}

func quoteHosts(config sitecfg.Result) []domain.Host {
	hosts := make([]domain.Host, 0)
	seen := make(map[string]bool)
	for _, section := range []string{"HQHOST", "TRYHQHOST"} {
		for _, host := range config.Hosts {
			if host.Section != section || host.IP == "" || host.Port < 1 || host.Port > 65535 {
				continue
			}
			item := domain.Host{IP: host.IP, Port: host.Port, Weight: host.Weight}
			if !seen[item.Address()] {
				seen[item.Address()] = true
				hosts = append(hosts, item)
			}
		}
	}
	return hosts
}

func probe(ctx context.Context, hosts []domain.Host, timeout time.Duration, factory SmokeFactory) []HostStatus {
	// 每条线路的处理流程：等并发名额 → 独占客户端 → 上海码表 → 样本逐笔 → 关闭连接 → 发布结果。
	results := make([]HostStatus, len(hosts))
	// 最多同时探测 8 条线路；各协程只写自己的结果槽位，全部结束后才统一排序。
	sem := make(chan struct{}, 8)
	var wg sync.WaitGroup
	for i, host := range hosts {
		wg.Add(1)
		go func() {
			defer wg.Done()
			select {
			case sem <- struct{}{}:
				defer func() { <-sem }()
			case <-ctx.Done():
				results[i] = HostStatus{Address: host.Address(), Error: "检测已取消"}
				return
			}
			started := time.Now()
			status := HostStatus{Address: host.Address()}
			client := factory(host, timeout)
			if client == nil {
				status.Error = "行情客户端未初始化"
			} else {
				if contextual, ok := client.(interface{ SetContext(context.Context) }); ok {
					contextual.SetContext(ctx)
				}
				count, err := client.SecurityCount(1)
				if err == nil && count < 1 {
					err = fmt.Errorf("上海码表数量为空")
				}
				status.Securities = count
				var ticks []domain.Transaction
				if err == nil {
					ticks, err = client.TransactionData(1, "600000", 0, 5)
				}
				if err == nil && len(ticks) == 0 {
					err = fmt.Errorf("600000 逐笔数据为空")
				}
				status.Ticks = len(ticks)
				status.OK = err == nil && ctx.Err() == nil
				if err != nil {
					status.Error = err.Error()
				}
				client.Close()
			}
			status.Latency = time.Since(started).Milliseconds()
			results[i] = status
		}()
	}
	wg.Wait()
	sort.SliceStable(results, func(i, j int) bool {
		if results[i].OK != results[j].OK {
			return results[i].OK
		}
		return results[i].Latency < results[j].Latency
	})
	return results
}
