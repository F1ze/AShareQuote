package service

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"quotes/internal/domain"
)

// FileBrowser 仅提供本机文件选择能力；具体路径访问和目录创建由平台适配器实现。
type FileBrowser interface {
	Browse(string, string) (domain.BrowseResult, error)
	CreateDirectory(string, string) (string, error)
}
type WorkspaceService struct {
	files     FileBrowser   // 本机路径适配器，未配置时明确返回能力不可用。
	lines     LineProvider  // 线路 TCP 初筛端口，不将 TCP 细节暴露给 Handler。
	newClient ClientFactory // 每条线路独占客户端，用真实行情响应验证端到端可用性。
}

func NewWorkspaceService(files FileBrowser, lines LineProvider, newClient ClientFactory) *WorkspaceService {
	return &WorkspaceService{files: files, lines: lines, newClient: newClient}
}
func (s *WorkspaceService) Browse(path, kind string) (domain.BrowseResult, error) {
	if s == nil || s.files == nil {
		return domain.BrowseResult{}, domain.ErrUnavailable
	}
	return s.files.Browse(path, kind)
}
func (s *WorkspaceService) CreateDirectory(path, name string) (string, error) {
	if s == nil || s.files == nil {
		return "", domain.ErrUnavailable
	}
	return s.files.CreateDirectory(path, name)
}

// TestLines 先做轻量 TCP 初筛，再以新连接完成握手和证券数量请求。
// 延迟覆盖拨号、协议握手、请求写入、完整响应读取与解码，不把本机隧道建连冒充远端测速。
func (s *WorkspaceService) TestLines(ctx context.Context, seconds int) (domain.LineReport, error) {
	if seconds == 0 {
		seconds = 8
	}
	if seconds < 2 || seconds > 60 {
		return domain.LineReport{}, fmt.Errorf("%w：超时须为 2–60 秒", domain.ErrInvalidInput)
	}
	if s == nil || s.lines == nil || s.newClient == nil {
		return domain.LineReport{}, domain.ErrUnavailable
	}
	timeout := time.Duration(seconds) * time.Second
	hosts, report, err := s.lines.Available(ctx, timeout)
	if err != nil {
		return report, err
	}
	testCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	report.Probe = "quote"
	report.CheckedAt = time.Now().UTC()
	report.Available = 0
	quoteResults := make([]domain.HostStatus, len(hosts))
	sem := make(chan struct{}, 8)
	var wg sync.WaitGroup
	for i, host := range hosts {
		wg.Add(1)
		go func() {
			defer wg.Done()
			status := domain.HostStatus{Name: host.Name, Address: host.Address()}
			defer func() { quoteResults[i] = status }()
			select {
			case sem <- struct{}{}:
				defer func() { <-sem }()
			case <-testCtx.Done():
				status.Error = testCtx.Err().Error()
				return
			}
			client := s.newClient([]domain.Host{host}, timeout, 0)
			if contextual, ok := client.(interface{ SetContext(context.Context) }); ok {
				contextual.SetContext(testCtx)
			}
			defer client.Close()
			started := time.Now()
			_, requestErr := client.SecurityCount(1)
			status.Latency = time.Since(started).Milliseconds()
			status.OK = requestErr == nil
			if requestErr != nil {
				status.Error = requestErr.Error()
			}
		}()
	}
	wg.Wait()

	// TCP 初筛失败项仍保留，避免只展示进入业务测速的线路。
	results := make([]domain.HostStatus, 0, len(report.Results))
	results = append(results, quoteResults...)
	for _, status := range report.Results {
		if !status.OK {
			results = append(results, status)
		}
	}
	sort.SliceStable(results, func(i, j int) bool {
		if results[i].OK != results[j].OK {
			return results[i].OK
		}
		return results[i].Latency < results[j].Latency
	})
	report.Results = results
	details := make([]string, 0, 3)
	for _, status := range results {
		if status.OK {
			report.Available++
		} else if len(details) < 3 {
			details = append(details, status.Address+"："+status.Error)
		}
	}
	if report.Available == 0 {
		return report, fmt.Errorf("全部线路行情测速失败：%s", strings.Join(details, "；"))
	}
	return report, nil
}
