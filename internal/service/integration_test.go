package service

import (
	"context"
	"encoding/binary"
	"net"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"quotes/internal/domain"
	"quotes/internal/platform/lines"
	"quotes/internal/platform/tdx"
)

func startDownloadTestServer(t *testing.T) Host {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	t.Cleanup(func() {
		_ = listener.Close()
		wg.Wait()
	})
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			wg.Add(1)
			go func() {
				defer wg.Done()
				defer conn.Close()
				if serveTestHandshake(conn) != nil {
					return
				}
				for {
					_, request, err := readTestFrame(conn)
					if err != nil {
						return
					}
					if len(request) != 28 || binary.LittleEndian.Uint16(request[:2]) != testBarsCommand {
						return
					}
					response := make([]byte, 2)
					binary.LittleEndian.PutUint16(response, 1)
					date := make([]byte, 4)
					binary.LittleEndian.PutUint32(date, 20240102)
					response = append(response, date...)
					response = appendTestPrice(response, 10000)
					response = appendTestPrice(response, 1000)
					response = appendTestPrice(response, 2000)
					response = appendTestPrice(response, -500)
					response = append(response, make([]byte, 8)...)
					if writeTestMessage(conn, testBarsCommand, response) != nil {
						return
					}
				}
			}()
		}
	}()
	host, err := parseHost(listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	return host
}

func TestManagerConcurrentDownload(t *testing.T) {
	host := startDownloadTestServer(t)
	output := t.TempDir()
	provider := lines.ProviderFunc(func(context.Context, time.Duration) ([]domain.Host, lines.Report, error) {
		return []domain.Host{host}, lines.Report{ConfigChecked: true, Tested: 1, Available: 1, Results: []domain.HostStatus{{Name: "本地测试站", Address: host.Address(), OK: true}}}, nil
	})
	factory := func(hosts []domain.Host, timeout time.Duration, offset int) Client {
		return tdx.NewClient(hosts, timeout, offset)
	}
	manager := newTestJobService(t, provider, output, factory)
	_, err := manager.Start(DownloadOptions{
		Codes: "600000,000001", Category: 9, Count: 10,
		Workers: 2, Timeout: 2, OutputDir: output,
	})
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(4 * time.Second)
	for time.Now().Before(deadline) {
		snapshot := manager.Snapshot()
		if snapshot != nil && snapshot.State == "completed_with_errors" {
			if snapshot.LineReport == nil || len(snapshot.LineReport.Results) != 1 || snapshot.LineReport.Results[0].Name != "本地测试站" {
				t.Fatalf("任务未发布自动检测报告：%+v", snapshot.LineReport)
			}
			// 调用者修改响应切片，不能改变下一次 SSE 快照的数据。
			snapshot.LineReport.Results[0].Name = "调用者改名"
			if manager.Snapshot().LineReport.Results[0].Name != "本地测试站" {
				t.Fatal("快照共享内部线路切片")
			}
			if snapshot.Succeeded != 2 || snapshot.Failed != 0 || snapshot.Bars != 2 {
				t.Fatalf("任务统计错误：%+v", snapshot)
			}
			for _, name := range []string{"sh600000_day.csv", "sz000001_day.csv"} {
				info, err := os.Stat(filepath.Join(output, name))
				if err != nil || info.Size() == 0 {
					t.Fatalf("输出文件 %s 无效：%v", name, err)
				}
			}
			return
		}
		if snapshot != nil && snapshot.State == "failed" {
			t.Fatalf("任务意外失败：%+v", snapshot)
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("并发下载任务未在限定时间内完成")
}
