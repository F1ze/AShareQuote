// Package lines turns the embedded site configuration into business-verified
// quote endpoints. TCP connectivity alone is never considered sufficient.
package lines

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"quotes/internal/core"
	"quotes/internal/sitecfg"
)

// Provider supplies validated, latency-ordered quote hosts.
type Provider interface {
	Available(ctx context.Context, timeout time.Duration) ([]core.Host, Report, error)
}

type ProviderFunc func(context.Context, time.Duration) ([]core.Host, Report, error)

func (f ProviderFunc) Available(ctx context.Context, timeout time.Duration) ([]core.Host, Report, error) {
	return f(ctx, timeout)
}

type HostStatus struct {
	Address    string `json:"address"`
	Latency    int64  `json:"latencyMs"`
	OK         bool   `json:"ok"`
	Securities int    `json:"securities,omitempty"`
	Ticks      int    `json:"ticks,omitempty"`
	Error      string `json:"error,omitempty"`
}

type Report struct {
	Source         string       `json:"source"`
	ConfigChecked  bool         `json:"configChecked"`
	ConfigBytes    int          `json:"configBytes"`
	ConfigWarnings int          `json:"configWarnings"`
	Total          int          `json:"total"`
	Tested         int          `json:"tested"`
	Available      int          `json:"available"`
	Results        []HostStatus `json:"results"`
}

// SmokeClient is the minimal business-protocol surface used by line checks.
type SmokeClient interface {
	Close()
	SecurityCount(market uint8) (int, error)
	TransactionData(market uint8, code string, start, count int) ([]core.Transaction, error)
}

type SmokeFactory func(host core.Host, timeout time.Duration) SmokeClient

type BuiltinProvider struct {
	spec    func() sitecfg.Spec
	factory SmokeFactory
}

func NewBuiltin(factory SmokeFactory) *BuiltinProvider {
	return NewBuiltinWithSpec(sitecfg.SeedSpec, factory)
}

func NewBuiltinWithSpec(spec func() sitecfg.Spec, factory SmokeFactory) *BuiltinProvider {
	return &BuiltinProvider{spec: spec, factory: factory}
}

// Available performs defaults -> build -> parse/check -> business smoke in one
// call. The generated connect.cfg remains in memory; no user file is required.
func (p *BuiltinProvider) Available(ctx context.Context, timeout time.Duration) ([]core.Host, Report, error) {
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
	report.Results = probe(ctx, hosts, timeout, p.factory)
	byAddress := make(map[string]core.Host, len(hosts))
	for _, host := range hosts {
		byAddress[host.Address()] = host
	}
	ordered := make([]core.Host, 0, len(hosts))
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

func quoteHosts(config sitecfg.Result) []core.Host {
	hosts := make([]core.Host, 0)
	seen := make(map[string]bool)
	for _, section := range []string{"HQHOST", "TRYHQHOST"} {
		for _, host := range config.Hosts {
			if host.Section != section || host.IP == "" || host.Port < 1 || host.Port > 65535 {
				continue
			}
			item := core.Host{IP: host.IP, Port: host.Port, Weight: host.Weight}
			if !seen[item.Address()] {
				seen[item.Address()] = true
				hosts = append(hosts, item)
			}
		}
	}
	return hosts
}

func probe(ctx context.Context, hosts []core.Host, timeout time.Duration, factory SmokeFactory) []HostStatus {
	results := make([]HostStatus, len(hosts))
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
				count, err := client.SecurityCount(1)
				if err == nil && count < 1 {
					err = fmt.Errorf("上海码表数量为空")
				}
				status.Securities = count
				var ticks []core.Transaction
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
