package service

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"quotes/internal/domain"
)

// concurrentFixture 用屏障要求第一批请求真正同时进入，避免把“启动了协程”误认为有效并发。
type concurrentFixture struct {
	active  atomic.Int32
	peak    atomic.Int32
	created atomic.Int32
	saved   atomic.Int32
	limit   int32
	barrier chan struct{}
	once    sync.Once
}

type boundedClient struct {
	fixture *concurrentFixture
	ctx     context.Context
	busy    atomic.Bool
}

func (c *boundedClient) Close()                                                  {}
func (c *boundedClient) SetContext(ctx context.Context)                          { c.ctx = ctx }
func (c *boundedClient) SecurityCount(uint8) (int, error)                        { return 0, nil }
func (c *boundedClient) SecurityList(uint8, int, int) ([]domain.Security, error) { return nil, nil }
func (c *boundedClient) SecurityBars(int, uint8, string, int, int) ([]domain.Bar, error) {
	if !c.busy.CompareAndSwap(false, true) {
		return nil, fmt.Errorf("同一客户端被并发使用")
	}
	defer c.busy.Store(false)
	active := c.fixture.active.Add(1)
	defer c.fixture.active.Add(-1)
	for previous := c.fixture.peak.Load(); active > previous; previous = c.fixture.peak.Load() {
		if c.fixture.peak.CompareAndSwap(previous, active) {
			break
		}
	}
	if active == c.fixture.limit {
		c.fixture.once.Do(func() { close(c.fixture.barrier) })
	}
	select {
	case <-c.ctx.Done():
		return nil, c.ctx.Err()
	case <-c.fixture.barrier:
	}
	return []domain.Bar{{Date: time.Date(2024, 1, 2, 0, 0, 0, 0, domain.ExchangeTime), Open: 10, High: 11, Low: 9, Close: 10}}, nil
}

type concurrentArchive struct{ fixture *concurrentFixture }

func (a concurrentArchive) Prepare() error { return nil }
func (a concurrentArchive) Verify(context.Context, domain.Checkpoint) (bool, error) {
	return false, nil
}
func (a concurrentArchive) Latest(context.Context, domain.Security, int) (time.Time, error) {
	return time.Time{}, nil
}
func (a concurrentArchive) State(context.Context, string) (domain.ArchiveState, error) {
	return domain.ArchiveState{}, nil
}
func (a concurrentArchive) Commit(_ context.Context, b domain.Batch) (domain.Receipt, error) {
	a.fixture.saved.Add(1)
	return domain.Receipt{Rows: len(b.Bars), From: "2024-01-02 00:00", To: "2024-01-02 00:00"}, nil
}

type boundedLines struct{ configured *atomic.Bool }

func (p boundedLines) Available(context.Context, time.Duration) ([]domain.Host, domain.LineReport, error) {
	if !p.configured.Load() {
		return nil, domain.LineReport{}, fmt.Errorf("探测前未配置流量上限")
	}
	return []domain.Host{{IP: "127.0.0.1", Port: 7709}}, domain.LineReport{Available: 1, Tested: 1}, nil
}

func TestWorkerPoolProcessesThousandSecuritiesWithBoundedParallelism(t *testing.T) {
	for _, tc := range []struct{ count, workers int }{{1000, 7}, {3, 64}} {
		t.Run(fmt.Sprintf("%d项_%d上限", tc.count, tc.workers), func(t *testing.T) {
			f := &concurrentFixture{limit: int32(min(tc.count, tc.workers)), barrier: make(chan struct{})}
			var configured atomic.Bool
			manager, err := NewJobService(JobDependencies{
				DefaultOutput: "unused", Lines: boundedLines{&configured},
				ConfigureTraffic: func(requestRate int) { configured.Store(requestRate == 40) },
				Archives:         func(string) Archive { return concurrentArchive{f} },
				NewClient:        func([]domain.Host, time.Duration, int) Client { f.created.Add(1); return &boundedClient{fixture: f} },
			})
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				ctx, cancel := context.WithTimeout(context.Background(), time.Second)
				defer cancel()
				manager.Shutdown(ctx)
			})
			codes := make([]string, tc.count)
			for i := range codes {
				codes[i] = fmt.Sprintf("sh%06d", 600000+i)
			}
			if _, err = manager.Start(DownloadOptions{Codes: strings.Join(codes, ","), Count: 1, Workers: tc.workers}); err != nil {
				t.Fatal(err)
			}
			deadline := time.NewTimer(5 * time.Second)
			defer deadline.Stop()
			for activeState(manager.Snapshot().State) {
				select {
				case <-deadline.C:
					t.Fatal("证券请求未能并发进入屏障或任务未收尾")
				case <-time.After(time.Millisecond):
				}
			}
			s := manager.Snapshot()
			if s.Succeeded != tc.count || s.Failed != 0 || f.saved.Load() != int32(tc.count) {
				t.Fatalf("处理丢失或失败：%+v", s)
			}
			if f.peak.Load() != f.limit || f.created.Load() != f.limit {
				t.Fatalf("并发未达到或超过上限：peak=%d clients=%d want=%d", f.peak.Load(), f.created.Load(), f.limit)
			}
		})
	}
}
