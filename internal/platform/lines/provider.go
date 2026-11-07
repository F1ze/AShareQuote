// Package lines 从内置配置生成候选线路，仅检查 TCP 连通性，不发送行情协议数据。
package lines

import (
	"context"
	"fmt"
	"net"
	"sort"
	"strings"
	"sync"
	"time"

	"quotes/internal/domain"
	"quotes/internal/platform/sitecfg"
)

// Provider 返回 TCP 建连成功、按耗时排序的线路，任务层无需了解配置来源与探测实现。
type Provider interface {
	Available(ctx context.Context, timeout time.Duration) ([]domain.Host, Report, error)
}

type ProviderFunc func(context.Context, time.Duration) ([]domain.Host, Report, error)

func (f ProviderFunc) Available(ctx context.Context, timeout time.Duration) ([]domain.Host, Report, error) {
	return f(ctx, timeout)
}

type HostStatus = domain.HostStatus
type Report = domain.LineReport

// DialFunc 注入应用统一拨号器；探测只调用 Dial/Close，不执行读写或行情握手。
type DialFunc func(context.Context, string, string) (net.Conn, error)

// RequestGate 可选地等待共享连接预算；等待阶段不占用 TCP 建连超时。
type RequestGate func(context.Context, domain.Host) error

type BuiltinProvider struct {
	spec func() sitecfg.Spec // 每次探测生成新的配置输入，避免在多次自检间共享可变配置。
	dial DialFunc            // 统一直连/代理拨号器，不构造通达信协议客户端。
	gate RequestGate         // 与下载共享的连接频率预算，测试可为空。
}

func NewBuiltin(dial DialFunc, gate RequestGate) *BuiltinProvider {
	return NewBuiltinWithSpec(sitecfg.SeedSpec, dial, gate)
}

func NewBuiltinWithSpec(spec func() sitecfg.Spec, dial DialFunc, gate RequestGate) *BuiltinProvider {
	return &BuiltinProvider{spec: spec, dial: dial, gate: gate}
}

// Available 依次完成默认配置生成、回读校验和 TCP 连通性探测；connect.cfg 仅在内存中流转。
// TCP 建连成功且上下文未取消即算通过；行情握手和业务验证留给真正下载阶段。
func (p *BuiltinProvider) Available(ctx context.Context, timeout time.Duration) ([]domain.Host, Report, error) {
	// 第 1 步：构建并回读配置，提取 HQHOST/TRYHQHOST 去重线路，最多安排前 24 条自检。
	report := Report{Probe: "tcp", Source: "builtin", CheckedAt: time.Now().UTC()}
	if p == nil || p.spec == nil || p.dial == nil {
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
	// 第 2 步：有界并发执行 TCP 探测，probe 等待全部结果并按可用性、总耗时排序。
	report.Results = probe(ctx, hosts, timeout, p.dial, p.gate)
	byAddress := make(map[string]domain.Host, len(hosts))
	for _, host := range hosts {
		byAddress[host.Address()] = host
	}
	// 第 3 步：只将 TCP 连通的 Host 返回给任务；全部失败时给出有限个诊断，不能继续盲下。
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
		return nil, report, fmt.Errorf("全部内置线路 TCP 连接失败：%s", strings.Join(details, "；"))
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
			item := domain.Host{Name: host.Name, IP: host.IP, Port: host.Port, Weight: host.Weight}
			if !seen[item.Address()] {
				seen[item.Address()] = true
				hosts = append(hosts, item)
			}
		}
	}
	return hosts
}

func probe(ctx context.Context, hosts []domain.Host, timeout time.Duration, dial DialFunc, gate RequestGate) []HostStatus {
	// 每个地址只拨号一次，不重试、不握手、不读写；限流等待在建立连接之前完成。
	if timeout <= 0 {
		timeout = 8 * time.Second
	}
	results := make([]HostStatus, len(hosts))
	sem := make(chan struct{}, 8)
	var wg sync.WaitGroup
	for i, host := range hosts {
		wg.Add(1)
		go func() {
			defer wg.Done()
			status := HostStatus{Name: host.Name, Address: host.Address()}
			defer func() { results[i] = status }()
			select {
			case sem <- struct{}{}:
				defer func() { <-sem }()
			case <-ctx.Done():
				status.Error = ctx.Err().Error()
				return
			}
			if gate != nil {
				if err := gate(ctx, host); err != nil {
					status.Error = err.Error()
					return
				}
			}
			if err := ctx.Err(); err != nil {
				status.Error = err.Error()
				return
			}
			// 计时只覆盖实际 TCP/代理隧道建立过程，不包含排队和限流等待。
			dialCtx, cancel := context.WithTimeout(ctx, timeout)
			defer cancel()
			started := time.Now()
			conn, err := dial(dialCtx, "tcp", host.Address())
			status.Latency = time.Since(started).Milliseconds()
			if conn != nil {
				_ = conn.Close()
			}
			if err == nil && conn == nil {
				err = fmt.Errorf("TCP 拨号未返回连接")
			}
			if err == nil {
				err = dialCtx.Err()
			}
			status.OK = err == nil
			if err != nil {
				status.Error = err.Error()
			}
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
