package lines

import (
	"context"
	"errors"
	"net"
	"sync/atomic"
	"testing"
	"time"

	"quotes/internal/domain"
	"quotes/internal/platform/sitecfg"
)

// 连接替身不提供行情应答；任何读写都会被计数，防止探测重新混入协议请求。
type probeConn struct {
	net.Conn
	reads  atomic.Int32
	writes atomic.Int32
	closes atomic.Int32
}

func newProbeConn() *probeConn {
	client, peer := net.Pipe()
	_ = peer.Close()
	return &probeConn{Conn: client}
}
func (c *probeConn) Read([]byte) (int, error) {
	c.reads.Add(1)
	return 0, errors.New("探测不得读取")
}
func (c *probeConn) Write([]byte) (int, error) {
	c.writes.Add(1)
	return 0, errors.New("探测不得写入")
}
func (c *probeConn) Close() error { c.closes.Add(1); return c.Conn.Close() }

func TestBuiltinOnlyConnectsAndCloses(t *testing.T) {
	spec := sitecfg.SeedSpec()
	spec.Hosts = spec.Hosts[:1]
	spec.TryHosts = nil
	conn := newProbeConn()
	calls := 0
	provider := NewBuiltinWithSpec(func() sitecfg.Spec { return spec }, func(_ context.Context, network, address string) (net.Conn, error) {
		calls++
		if network != "tcp" || address != net.JoinHostPort(spec.Hosts[0].IP, "7709") {
			t.Errorf("拨号参数错误：%s %s", network, address)
		}
		return conn, nil
	}, nil)
	hosts, report, err := provider.Available(context.Background(), time.Second)
	if err != nil || len(hosts) != 1 || report.Available != 1 || report.Probe != "tcp" || !report.ConfigChecked || report.ConfigBytes == 0 {
		t.Fatalf("TCP 检测失败：%+v %v", report, err)
	}
	if calls != 1 || conn.reads.Load() != 0 || conn.writes.Load() != 0 || conn.closes.Load() != 1 {
		t.Fatalf("探测必须只拨号一次并关闭，不读写：calls=%d conn=%+v", calls, conn)
	}
}

func TestBuiltinRejectsTCPFailureWithoutRetry(t *testing.T) {
	var calls atomic.Int32
	provider := NewBuiltin(func(context.Context, string, string) (net.Conn, error) {
		calls.Add(1)
		return nil, errors.New("connection refused")
	}, nil)
	_, report, err := provider.Available(context.Background(), time.Second)
	if err == nil || report.Available != 0 || int(calls.Load()) != report.Tested {
		t.Fatalf("失败应每站只尝试一次：%+v calls=%d err=%v", report, calls.Load(), err)
	}
}

func TestProbeHonorsTimeoutAndGateCancellation(t *testing.T) {
	hosts := []domain.Host{{IP: "127.0.0.1", Port: 7709}}
	statuses := probe(context.Background(), hosts, 10*time.Millisecond, func(ctx context.Context, _, _ string) (net.Conn, error) { <-ctx.Done(); return nil, ctx.Err() }, nil)
	if statuses[0].OK || statuses[0].Error != context.DeadlineExceeded.Error() {
		t.Fatalf("TCP 建连超时未生效：%+v", statuses)
	}
	ctx, cancel := context.WithCancel(context.Background())
	var calls atomic.Int32
	statuses = probe(ctx, hosts, time.Second, func(context.Context, string, string) (net.Conn, error) { calls.Add(1); return newProbeConn(), nil }, func(ctx context.Context, _ domain.Host) error { cancel(); return ctx.Err() })
	if statuses[0].OK || statuses[0].Error != context.Canceled.Error() || calls.Load() != 0 {
		t.Fatalf("限流取消后不得拨号：%+v", statuses)
	}
}

// 用屏障锁住第一批拨号，确认没有第九条连接越过并发上限。
func TestProbeBoundsConcurrentConnections(t *testing.T) {
	hosts := make([]domain.Host, 13)
	for i := range hosts {
		hosts[i] = domain.Host{IP: "127.0.0.1", Port: 7709 + i}
	}
	entered := make(chan struct{}, len(hosts))
	release := make(chan struct{})
	done := make(chan []HostStatus, 1)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	go func() {
		done <- probe(ctx, hosts, time.Second, func(ctx context.Context, _, _ string) (net.Conn, error) {
			entered <- struct{}{}
			select {
			case <-release:
				return newProbeConn(), nil
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		}, nil)
	}()
	for range 8 {
		select {
		case <-entered:
		case <-ctx.Done():
			t.Fatal("未启动预期的并发拨号")
		}
	}
	select {
	case <-entered:
		t.Error("超过 8 条并发连接")
	case <-time.After(20 * time.Millisecond):
	}
	close(release)
	select {
	case results := <-done:
		for _, status := range results {
			if !status.OK {
				t.Errorf("拨号失败：%+v", status)
			}
		}
	case <-ctx.Done():
		t.Fatal("探测未结束")
	}
}

// 主备站去重后必须探测全部 13 个行情地址，IPv6 用方括号连接端口，其他业务端口不得混入。
func TestBuiltinProbesAllSeedQuoteEndpoints(t *testing.T) {
	provider := NewBuiltin(func(context.Context, string, string) (net.Conn, error) { return newProbeConn(), nil }, nil)
	hosts, report, err := provider.Available(context.Background(), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if report.Total != 13 || report.Tested != 13 || len(hosts) != 13 {
		t.Fatalf("候选线路遗漏或重复：%+v", report)
	}
	addresses := make(map[string]bool)
	if report.CheckedAt.IsZero() {
		t.Fatal("检测报告缺少时间")
	}
	for _, host := range hosts {
		if host.Name == "" || host.Name == "站" {
			t.Fatalf("线路缺少真实名称：%+v", host)
		}
		if host.Port != 7709 {
			t.Fatalf("混入非行情站：%s", host.Address())
		}
		addresses[host.Address()] = true
	}
	for _, status := range report.Results {
		if status.Name == "" || !addresses[status.Address] {
			t.Fatalf("检测结果名称或地址丢失：%+v", status)
		}
	}
	for _, address := range []string{"114.141.177.40:7709", "1.202.143.37:7709", "111.203.134.118:7709", "117.133.128.226:7709", "[2402:4e00:1600:1524:6:5989:fc84:a9b3]:7709", "[2408:4003:1f20::1e6]:7709"} {
		if !addresses[address] {
			t.Errorf("遗漏行情地址：%s", address)
		}
	}
}
