package service

import (
	"context"

	"quotes/internal/domain"
)

// WarehouseQueries 是研究读取端口。每个方法对应明确用例，不向服务暴露 SQL 或 Python op。
type WarehouseQueries interface {
	Summary(context.Context) (domain.WarehouseSummary, error)
	Datasets(context.Context, domain.DatasetQuery) (domain.DatasetPage, error)
	Bars(context.Context, domain.BarQuery) ([]domain.WarehouseBar, error)
	Gaps(context.Context) ([]domain.GapCandidate, error)
	Export(context.Context) (domain.ExportResult, error)
	ImportReference(context.Context, domain.ReferenceImport) (domain.ImportResult, error)
}

// WarehouseService 校验查询范围并协调研究仓库；既不处理 URL/HTTP，也不读文件或拼 SQL。
type WarehouseService struct {
	repository WarehouseQueries // 研究查询与导入端口，nil 时该服务明确报告不可用。
}

func NewWarehouseService(repository WarehouseQueries) *WarehouseService {
	return &WarehouseService{repository: repository}
}
func (s *WarehouseService) Enabled() bool { return s != nil && s.repository != nil }
func (s *WarehouseService) Summary(ctx context.Context) (domain.WarehouseSummary, error) {
	if !s.Enabled() {
		return domain.WarehouseSummary{}, domain.ErrUnavailable
	}
	return s.repository.Summary(ctx)
}
func (s *WarehouseService) Datasets(ctx context.Context, q domain.DatasetQuery) (domain.DatasetPage, error) {
	q, err := q.Normalize()
	if err != nil {
		return domain.DatasetPage{}, err
	}
	if !s.Enabled() {
		return domain.DatasetPage{}, domain.ErrUnavailable
	}
	return s.repository.Datasets(ctx, q)
}
func (s *WarehouseService) Bars(ctx context.Context, q domain.BarQuery) ([]domain.WarehouseBar, error) {
	q, err := q.Normalize()
	if err != nil {
		return nil, err
	}
	if !s.Enabled() {
		return nil, domain.ErrUnavailable
	}
	return s.repository.Bars(ctx, q)
}
func (s *WarehouseService) Gaps(ctx context.Context) ([]domain.GapCandidate, error) {
	if !s.Enabled() {
		return nil, domain.ErrUnavailable
	}
	return s.repository.Gaps(ctx)
}
func (s *WarehouseService) Export(ctx context.Context) (domain.ExportResult, error) {
	if !s.Enabled() {
		return domain.ExportResult{}, domain.ErrUnavailable
	}
	return s.repository.Export(ctx)
}
func (s *WarehouseService) ImportReference(ctx context.Context, r domain.ReferenceImport) (domain.ImportResult, error) {
	if err := r.Validate(); err != nil {
		return domain.ImportResult{}, err
	}
	if !s.Enabled() {
		return domain.ImportResult{}, domain.ErrUnavailable
	}
	return s.repository.ImportReference(ctx, r)
}
