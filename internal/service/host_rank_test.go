package service

import (
	"context"
	"errors"
	"testing"
	"time"
)

type rankTestClient struct {
	ctx   context.Context
	delay time.Duration
	err   error
}

func (c *rankTestClient) Close()                         {}
func (c *rankTestClient) SetContext(ctx context.Context) { c.ctx = ctx }
func (c *rankTestClient) wait() error {
	if c.delay > 0 {
		timer := time.NewTimer(c.delay)
		defer timer.Stop()
		select {
		case <-timer.C:
		case <-c.ctx.Done():
			return c.ctx.Err()
		}
	}
	return c.err
}
func (c *rankTestClient) SecurityCount(uint8) (int, error) {
	if err := c.wait(); err != nil {
		return 0, err
	}
	return 1, nil
}
func (c *rankTestClient) SecurityList(uint8, int, int) ([]Security, error) {
	return nil, nil
}
func (c *rankTestClient) SecurityBars(int, uint8, string, int, int) ([]Bar, error) {
	if err := c.wait(); err != nil {
		return nil, err
	}
	return dailyBars(1), nil
}

func TestRankHostsUsesBusinessLatencyAndKeepsFailuresAsFallback(t *testing.T) {
	task := &jobRun{
		ctx: context.Background(), options: DownloadOptions{Category: 9, Timeout: 3},
		hosts: []Host{{IP: "192.0.2.1", Port: 7709}, {IP: "192.0.2.2", Port: 7709}, {IP: "192.0.2.3", Port: 7709}},
	}
	task.newClient = func(hosts []Host, _ time.Duration, _ int) Client {
		switch hosts[0].IP {
		case "192.0.2.1":
			return &rankTestClient{delay: 20 * time.Millisecond}
		case "192.0.2.2":
			return &rankTestClient{err: errors.New("EOF")}
		default:
			return &rankTestClient{delay: time.Millisecond}
		}
	}
	if preferred := task.rankHosts(Security{Market: 1, Code: "600000"}); preferred != 2 {
		t.Fatalf("首选线路数 %d", preferred)
	}
	if task.hosts[0].IP != "192.0.2.3" || task.hosts[1].IP != "192.0.2.1" || task.hosts[2].IP != "192.0.2.2" {
		t.Fatalf("线路排序或后备丢失：%+v", task.hosts)
	}
	if task.pages != 2 {
		t.Fatalf("成功测速页数 %d", task.pages)
	}
}

func TestRankHostsStopsAfterEightSuccessfulProbes(t *testing.T) {
	task := &jobRun{
		ctx: context.Background(), options: DownloadOptions{Category: 9, Timeout: 3},
		hosts: []Host{
			{IP: "192.0.2.1", Port: 7709}, {IP: "192.0.2.2", Port: 7709}, {IP: "192.0.2.3", Port: 7709},
			{IP: "192.0.2.4", Port: 7709}, {IP: "192.0.2.5", Port: 7709}, {IP: "192.0.2.6", Port: 7709},
			{IP: "192.0.2.7", Port: 7709}, {IP: "192.0.2.8", Port: 7709}, {IP: "192.0.2.9", Port: 7709},
		},
	}
	task.newClient = func(hosts []Host, _ time.Duration, _ int) Client {
		if hosts[0].IP == "192.0.2.9" {
			return &rankTestClient{delay: 5 * time.Second}
		}
		return &rankTestClient{delay: time.Millisecond}
	}
	started := time.Now()
	if preferred := task.rankHosts(Security{Market: 1, Code: "600000"}); preferred != 8 {
		t.Fatalf("首选线路数 %d", preferred)
	}
	if elapsed := time.Since(started); elapsed >= time.Second {
		t.Fatalf("凑齐八条线路后仍等待慢线路：%s", elapsed)
	}
	if task.pages != 8 {
		t.Fatalf("成功测速页数 %d", task.pages)
	}
}
