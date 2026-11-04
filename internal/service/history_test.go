package service

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"testing"
	"time"
)

type historyClient struct {
	Client
	bars     []Bar
	calls    [][2]int
	failAt   int
	closed   bool
	repeat   bool
	pageSize int
}

func (c *historyClient) Close() { c.closed = true }
func (c *historyClient) SecurityBars(_ int, _ uint8, _ string, start, count int) ([]Bar, error) {
	c.calls = append(c.calls, [2]int{start, count})
	if c.failAt > 0 && len(c.calls) == c.failAt {
		return nil, errors.New("broken stream")
	}
	if c.repeat {
		start = 0
	}
	if c.pageSize > 0 {
		count = min(count, c.pageSize)
	}
	end := max(0, len(c.bars)-start)
	begin := max(0, end-count)
	return append([]Bar(nil), c.bars[begin:end]...), nil
}

func dailyBars(n int) []Bar {
	bars := make([]Bar, n)
	from := time.Date(2020, 1, 1, 0, 0, 0, 0, exchangeTime)
	for i := range bars {
		bars[i] = Bar{Date: from.AddDate(0, 0, i), Open: 10, High: 12, Low: 9, Close: 11}
	}
	return bars
}

func historyTask(from, to string) *jobRun {
	return &jobRun{ctx: context.Background(), options: DownloadOptions{Category: 9, StartDate: from, EndDate: to}}
}

func TestHistorySpansMoreThan800AndFiltersDates(t *testing.T) {
	client := &historyClient{bars: dailyBars(2100)}
	from, to := client.bars[100].Date.Format("2006-01-02"), client.bars[1900].Date.Format("2006-01-02")
	task := historyTask(from, to)
	bars, warning, err := task.fetchBars(client, Security{Code: "600000", Market: 1})
	if err != nil || warning != "" || len(bars) != 1801 {
		t.Fatalf("got %d bars, warning %q, error %v", len(bars), warning, err)
	}
	if len(client.calls) != 3 || client.calls[1][0] != 800 || client.calls[2][0] != 1600 {
		t.Fatalf("wrong pagination: %v", client.calls)
	}
	if bars[0].Date.Format("2006-01-02") != from || bars[len(bars)-1].Date.Format("2006-01-02") != to {
		t.Fatal("inclusive date filtering failed")
	}
	for i := 1; i < len(bars); i++ {
		if !bars[i].Date.After(bars[i-1].Date) {
			t.Fatal("output not strictly ordered")
		}
	}
}

func TestMinuteHistoryIncludesEndDayAndSmallPages(t *testing.T) {
	base := time.Date(2024, 1, 2, 9, 30, 0, 0, exchangeTime)
	client := &historyClient{pageSize: 200}
	for day := 0; day < 3; day++ {
		for minute := 0; minute < 600; minute++ {
			client.bars = append(client.bars, Bar{Date: base.AddDate(0, 0, day).Add(time.Duration(minute) * time.Minute)})
		}
	}
	task := historyTask("2024-01-03", "2024-01-04")
	task.options.Category = 7
	bars, warning, err := task.fetchBars(client, Security{})
	if err != nil || warning != "" || len(bars) != 1200 {
		t.Fatalf("got %d, %q, %v", len(bars), warning, err)
	}
	if client.calls[1][0] != 200 || bars[len(bars)-1].Date.Day() != 4 {
		t.Fatal("small page / inclusive end failed")
	}
}

func TestHistoryExhaustionAndRepeatedPagesWarn(t *testing.T) {
	for _, repeat := range []bool{false, true} {
		t.Run(map[bool]string{false: "exhausted", true: "repeated"}[repeat], func(t *testing.T) {
			client := &historyClient{bars: dailyBars(900), repeat: repeat}
			task := historyTask("2019-01-01", "2024-01-01")
			bars, warning, err := task.fetchBars(client, Security{})
			if err != nil || warning == "" || len(bars) == 0 || len(client.calls) > 3 {
				t.Fatalf("got %d, %q, %v, calls %v", len(bars), warning, err, client.calls)
			}
			for i := 1; i < len(bars); i++ {
				if !bars[i].Date.After(bars[i-1].Date) {
					t.Fatal("duplicates remain")
				}
			}
		})
	}
}

func TestHistoryFailureAndEmptyDoNotOverwrite(t *testing.T) {
	for _, scenario := range []string{"second page fails", "empty range", "cancelled"} {
		t.Run(scenario, func(t *testing.T) {
			dir := t.TempDir()
			security := Security{Code: "600000", Market: 1}
			name := filepath.Join(dir, csvFileName(security, 9))
			if err := os.WriteFile(name, []byte("existing complete data"), 0600); err != nil {
				t.Fatal(err)
			}
			task := historyTask("2020-01-01", "2024-01-01")
			task.output = dir
			task.options.Timeout = 2
			client := &historyClient{bars: dailyBars(1800)}
			if scenario == "second page fails" {
				client.failAt = 2
			}
			if scenario == "empty range" {
				task.options.StartDate = "2018-01-01"
				task.options.EndDate = "2019-01-01"
			}
			if scenario == "cancelled" {
				ctx, cancel := context.WithCancel(context.Background())
				cancel()
				task.ctx = ctx
			}
			task.newClient = func([]Host, time.Duration, int) Client { return client }
			jobs := make(chan Security, 1)
			jobs <- security
			close(jobs)
			task.worker(0, jobs)
			data, err := os.ReadFile(name)
			if err != nil || string(data) != "existing complete data" {
				t.Fatalf("old CSV replaced: %s %v", data, err)
			}
		})
	}
}

func TestHistoryLegacyCountAndOffsetBoundary(t *testing.T) {
	client := &historyClient{bars: dailyBars(1800)}
	task := &jobRun{ctx: context.Background(), options: DownloadOptions{Count: 1601}}
	bars, warning, err := task.fetchBars(client, Security{})
	if err != nil || warning != "" || len(bars) != 1601 || client.calls[2][1] != 1 {
		t.Fatalf("legacy pagination: %d %q %v %v", len(bars), warning, err, client.calls)
	}
	client = &historyClient{bars: dailyBars(65536)}
	task = historyTask("1900-01-01", "2024-01-01")
	task.options.Start = 65535
	_, warning, err = task.fetchBars(client, Security{})
	if err != nil || warning == "" || len(client.calls) != 1 || client.calls[0] != [2]int{65535, 1} {
		t.Fatalf("offset boundary: %v %q %v", client.calls, warning, err)
	}
}

func TestInvalidDatesRejectedBeforeTaskStarts(t *testing.T) {
	for _, options := range []DownloadOptions{
		{StartDate: "2024-02-30", EndDate: "2024-03-01"},
		{StartDate: "2024-03-02", EndDate: "2024-03-01"},
		{StartDate: "2024-01-01"},
		{StartDate: "2024-01-01", EndDate: "2999-01-01"},
		{Start: 65536, Count: 1},
		{Markets: []int{-1}},
	} {
		options.Codes = "600000"
		options.Category = 9
		manager := newTestJobService(t, nil, t.TempDir(), nil)
		if _, err := manager.Start(options); err == nil {
			t.Fatalf("invalid options accepted: %+v", options)
		}
		if manager.Snapshot() != nil {
			t.Fatal("invalid request started a task")
		}
	}
}

type discoveryClient struct {
	Client
	calls  []int
	repeat bool
}

func (c *discoveryClient) Close()                           {}
func (c *discoveryClient) SecurityCount(uint8) (int, error) { return 5, nil }
func (c *discoveryClient) SecurityList(market uint8, start, count int) ([]Security, error) {
	c.calls = append(c.calls, start)
	if c.repeat {
		start = 0
	}
	var out []Security
	for i := start; i < min(start+2, 5); i++ {
		out = append(out, Security{Market: market, Code: "00000" + strconv.Itoa(i)})
	}
	return out, nil
}

func TestDiscoveryUsesActualPageLength(t *testing.T) {
	for _, repeat := range []bool{false, true} {
		client := &discoveryClient{repeat: repeat}
		task := &jobRun{ctx: context.Background(), hosts: []Host{{IP: "127.0.0.1"}}, newClient: func([]Host, time.Duration, int) Client { return client }}
		securities, err := task.discoverMarket(0)
		if repeat {
			if err == nil {
				t.Fatal("repeated security page accepted")
			}
			continue
		}
		if err != nil || len(securities) != 5 || !reflect.DeepEqual(client.calls, []int{0, 2, 4}) {
			t.Fatalf("discovery skipped securities: %+v %v %v", securities, client.calls, err)
		}
	}
}
