package service

import (
	"context"
	"fmt"
	"time"

	"quotes/internal/domain"
)

// FileBrowser 仅提供本机文件选择能力；具体路径访问和目录创建由平台适配器实现。
type FileBrowser interface {
	Browse(string, string) (domain.BrowseResult, error)
	CreateDirectory(string, string) (string, error)
}
type WorkspaceService struct {
	files FileBrowser  // 本机路径适配器，未配置时明确返回能力不可用。
	lines LineProvider // 线路业务探测端口，不将 TCP 细节暴露给 Handler。
}

func NewWorkspaceService(files FileBrowser, lines LineProvider) *WorkspaceService {
	return &WorkspaceService{files: files, lines: lines}
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
func (s *WorkspaceService) TestLines(ctx context.Context, seconds int) (domain.LineReport, error) {
	if seconds == 0 {
		seconds = 8
	}
	if seconds < 2 || seconds > 60 {
		return domain.LineReport{}, fmt.Errorf("%w：超时须为 2–60 秒", domain.ErrInvalidInput)
	}
	if s == nil || s.lines == nil {
		return domain.LineReport{}, domain.ErrUnavailable
	}
	_, report, err := s.lines.Available(ctx, time.Duration(seconds)*time.Second)
	return report, err
}
