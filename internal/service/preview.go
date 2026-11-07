package service

import (
	"context"
	"fmt"

	"quotes/internal/domain"
)

// PreviewRepository 读取原始归档和独立事件，不在仓库中决定复权模式或显示窗口。
type PreviewRepository interface {
	ReadBars(context.Context, string) ([]domain.Bar, error)
	ReadAdjustment(context.Context, string, bool) ([]domain.AdjustmentEvent, bool, error)
}

// PreviewService 组织“读取全量→领域复权→截显示窗口”，数据库和 CSV 的原价均不会被改写。
type PreviewService struct {
	output   func() string                  // 当前任务的归档目录，由组装根注入，不依赖具体任务实现。
	archives func(string) PreviewRepository // 按目录创建只读归档端口，不在服务里选文件格式。
}

func NewPreviewService(output func() string, archives func(string) PreviewRepository) *PreviewService {
	return &PreviewService{output: output, archives: archives}
}
func (s *PreviewService) Preview(ctx context.Context, q domain.PreviewQuery) (domain.PreviewResponse, error) {
	// 1. 校验逻辑文件名、模式及窗口；不向仓库传入任意浏览器路径。
	q, err := q.Normalize()
	if err != nil {
		return domain.PreviewResponse{}, err
	}
	if s == nil || s.output == nil || s.archives == nil {
		return domain.PreviewResponse{}, domain.ErrUnavailable
	}
	repo := s.archives(s.output())
	bars, err := repo.ReadBars(ctx, q.File)
	if err != nil {
		return domain.PreviewResponse{}, err
	}
	// 2. raw 只探测附件存在性；复权模式才解析事件，避免无关坏因子阻断原价查看。
	events, available, err := repo.ReadAdjustment(ctx, q.File, q.Mode != "raw")
	if err != nil {
		return domain.PreviewResponse{}, err
	}
	if q.Mode != "raw" && !available {
		return domain.PreviewResponse{}, fmt.Errorf("%w：未找到该证券的复权因子文件，请确认下载时已配置 gbbq", domain.ErrNotFound)
	}
	// 3. 必须对完整序列计算后复权基准，再截取尾部窗口，否则价格会随 limit 改变。
	adjusted, err := domain.AdjustBars(bars, events, q.Mode)
	if err != nil {
		return domain.PreviewResponse{}, fmt.Errorf("%w：%v", domain.ErrInvalidInput, err)
	}
	result := domain.PreviewResponse{File: q.File, Mode: q.Mode, Total: len(adjusted), FactorAvailable: available, FactorEvents: len(events), Bars: []domain.PreviewBar{}}
	if len(adjusted) > 0 {
		result.From = adjusted[0].Date.Format("2006-01-02 15:04")
		result.To = adjusted[len(adjusted)-1].Date.Format("2006-01-02 15:04")
	}
	if !q.All && len(adjusted) > q.Limit {
		adjusted = adjusted[len(adjusted)-q.Limit:]
	}
	for _, bar := range adjusted {
		if err := ctx.Err(); err != nil {
			return domain.PreviewResponse{}, err
		}
		layout := "2006-01-02"
		if bar.Date.Hour() != 0 || bar.Date.Minute() != 0 {
			layout = "2006-01-02 15:04"
		}
		result.Bars = append(result.Bars, domain.PreviewBar{Date: bar.Date.Format(layout), Open: bar.Open, High: bar.High, Low: bar.Low, Close: bar.Close, Volume: bar.Volume, Amount: bar.Amount})
	}
	return result, nil
}
