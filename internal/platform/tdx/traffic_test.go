package tdx

import (
	"context"
	"errors"
	"fmt"
	"net"
	"slices"
	"sync"
	"testing"
	"time"
)

func TestClientOwnsIndependentShuffledCandidates(t *testing.T) {
	hosts := make([]Host, 13)
	for i := range hosts {
		hosts[i] = Host{IP: fmt.Sprintf("192.0.2.%d", i+1), Port: 7709}
	}
	original := slices.Clone(hosts)
	c := NewClient(hosts, time.Second, -1)
	if !slices.Equal(hosts, original) {
		t.Fatal("打乱了调用方共享切片")
	}
	if c.offset < 0 || c.offset >= len(hosts) {
		t.Fatal("负偏移未规范化")
	}
	// 验证实际拨号遍历的是该客户端保存的排列，每个候选只尝试一次，不靠概率断言顺序必然改变。
	want := make([]string, 0, len(hosts))
	for i := range c.hosts {
		want = append(want, c.hosts[(c.offset+i)%len(c.hosts)].Address())
	}
	var seen []string
	c.SetDialer(func(_ context.Context, _, address string) (net.Conn, error) {
		seen = append(seen, address)
		return nil, errors.New("offline fixture")
	})
	if c.connect() == nil || !slices.Equal(seen, want) {
		t.Fatalf("候选遍历遗漏或重复：%v", seen)
	}
	c.hosts[0].IP = "changed"
	if !slices.Equal(hosts, original) {
		t.Fatal("客户端仍持有调用方底层数组")
	}
}

func TestTrafficSharedGlobalAndHostBudgets(t *testing.T) {
	for _, tc := range []struct {
		name            string
		global, perHost int
		sameHost        bool
	}{
		{"全局跨站共享", 50, 1000, false}, {"单站跨连接共享", 1000, 50, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			traffic := NewTrafficController(tc.global, tc.perHost)
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			start := time.Now()
			var wg sync.WaitGroup
			for i := 0; i < 10; i++ {
				wg.Add(1)
				go func(i int) {
					defer wg.Done()
					host := Host{IP: fmt.Sprintf("192.0.2.%d", i+1), Port: 7709}
					if tc.sameHost {
						host.IP = "192.0.2.1"
					}
					if err := traffic.Wait(ctx, host); err != nil {
						t.Error(err)
					}
				}(i)
			}
			wg.Wait()
			if time.Since(start) < 175*time.Millisecond {
				t.Fatal("并发调用绕过共享频率上限")
			}
		})
	}
}

func TestTrafficCancellationDoesNotReserveFutureBudget(t *testing.T) {
	traffic := NewTrafficController(1000, 1)
	host := Host{IP: "192.0.2.1", Port: 7709}
	if err := traffic.Wait(context.Background(), host); err != nil {
		t.Fatal(err)
	}
	before := traffic.nextByHost[host.Address()]
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- traffic.Wait(ctx, host) }()
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("取消未中断限流等待")
	}
	if traffic.nextByHost[host.Address()] != before {
		t.Fatal("取消请求占用了未来单站额度")
	}
	// 被单站限流的连接不应阻塞其他站点。
	ctx2, cancel2 := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel2()
	if err := traffic.Wait(ctx2, Host{IP: "192.0.2.2", Port: 7709}); err != nil {
		t.Fatal(err)
	}
	traffic.Configure(200, 2)
	if traffic.nextByHost[host.Address()] != before {
		t.Fatal("配置更新重置了已消费预算")
	}
}
