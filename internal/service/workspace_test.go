package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"quotes/internal/domain"
)

type workspaceProviderFunc func(context.Context, time.Duration) ([]domain.Host, domain.LineReport, error)

func (f workspaceProviderFunc) Available(ctx context.Context, timeout time.Duration) ([]domain.Host, domain.LineReport, error) {
	return f(ctx, timeout)
}

func TestWorkspaceTestLinesMeasuresCompleteQuoteRoundTrip(t *testing.T) {
	hosts := []domain.Host{{IP: "192.0.2.1", Port: 7709}, {IP: "192.0.2.2", Port: 7709}, {IP: "192.0.2.3", Port: 7709}}
	provider := workspaceProviderFunc(func(context.Context, time.Duration) ([]domain.Host, domain.LineReport, error) {
		return hosts, domain.LineReport{
			Probe: "tcp", Tested: 4, Available: 3,
			Results: []domain.HostStatus{
				{Address: "192.0.2.1:7709", OK: true}, {Address: "192.0.2.2:7709", OK: true},
				{Address: "192.0.2.3:7709", OK: true}, {Address: "192.0.2.4:7709", Latency: 5, Error: "TCP 连接失败"},
			},
		}, nil
	})
	workspace := NewWorkspaceService(nil, provider, func(hosts []domain.Host, _ time.Duration, _ int) Client {
		switch hosts[0].IP {
		case "192.0.2.1":
			return &rankTestClient{delay: 20 * time.Millisecond}
		case "192.0.2.2":
			return &rankTestClient{err: errors.New("EOF")}
		default:
			return &rankTestClient{delay: time.Millisecond}
		}
	})
	report, err := workspace.TestLines(context.Background(), 2)
	if err != nil {
		t.Fatal(err)
	}
	if report.Probe != "quote" || report.Available != 2 || report.Tested != 4 || len(report.Results) != 4 {
		t.Fatalf("完整行情测速统计错误：%+v", report)
	}
	if report.Results[0].Address != "192.0.2.3:7709" || report.Results[1].Address != "192.0.2.1:7709" {
		t.Fatalf("成功线路未按真实往返耗时排序：%+v", report.Results)
	}
	failures := make(map[string]string)
	for _, status := range report.Results {
		if !status.OK {
			failures[status.Address] = status.Error
		}
	}
	if failures["192.0.2.2:7709"] != "EOF" || failures["192.0.2.4:7709"] != "TCP 连接失败" {
		t.Fatalf("行情失败或 TCP 初筛失败未保留：%+v", report.Results)
	}
}

func TestWorkspaceTestLinesDoesNotAcceptTCPOnlySuccess(t *testing.T) {
	host := domain.Host{IP: "192.0.2.1", Port: 7709}
	provider := workspaceProviderFunc(func(context.Context, time.Duration) ([]domain.Host, domain.LineReport, error) {
		return []domain.Host{host}, domain.LineReport{Probe: "tcp", Tested: 1, Available: 1, Results: []domain.HostStatus{{Address: host.Address(), OK: true}}}, nil
	})
	workspace := NewWorkspaceService(nil, provider, func([]domain.Host, time.Duration, int) Client {
		return &rankTestClient{err: errors.New("EOF")}
	})
	report, err := workspace.TestLines(context.Background(), 2)
	if err == nil || report.Probe != "quote" || report.Available != 0 {
		t.Fatalf("TCP 成功不能冒充行情测速成功：%+v %v", report, err)
	}
}
