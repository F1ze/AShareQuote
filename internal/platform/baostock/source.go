// Package baostock 将官方 Python SDK 适配为 Go 历史来源，包含来源提供的历史及退市名录。
// SDK 会话隔离在独立进程中，不与 Go 仓库共享 SDK 会话或故障状态。
package baostock

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"quotes/internal/domain"
	"quotes/internal/platform/bridge"
	"quotes/internal/service"
)

type Source struct {
	*bridge.Process // 独立 SDK 会话进程桥接，按需启动，串行请求，取消后重建；不持有仓库连接。
}

// Registration 声明当前适配范围：沪深，日/周/月及 5/15/30/60 分钟；不支持北交所或 1 分钟。
func (s *Source) Registration() service.SourceSpec {
	return service.SourceSpec{Key: "baostock", Label: "BaoStock", Markets: []int{0, 1}, Categories: []int{0, 1, 2, 3, 4, 5, 6, 9}, Adapter: s}
}

// read 限制一次完整 SDK 操作（含内部翻页）的总时长，防止 SDK 断连后长期阻塞任务。
// 超时会沿桥接层终止进程，下次调用重建会话。
func (s *Source) read(ctx context.Context, request any) (json.RawMessage, error) {
	bounded, cancel := context.WithTimeout(ctx, 120*time.Second)
	defer cancel()
	return s.Call(bounded, request)
}

func New(project string) *Source {
	return &Source{bridge.New(project, "internal/platform/baostock/source_worker.py")}
}
func (s *Source) Universe(ctx context.Context, markets []int) ([]domain.Security, error) {
	data, err := s.read(ctx, map[string]any{"op": "universe", "markets": markets})
	if err != nil {
		return nil, err
	}
	var result []domain.Security
	err = json.Unmarshal(data, &result)
	return result, err
}
func (s *Source) Calendar(ctx context.Context, from, to string) ([]domain.TradingDay, error) {
	data, err := s.read(ctx, map[string]any{"op": "calendar", "from": from, "to": to})
	if err != nil {
		return nil, err
	}
	var result []domain.TradingDay
	err = json.Unmarshal(data, &result)
	return result, err
}

// Factors 保留供应商十进制字符串与原始语义，避免先转 float64 丢失因子精度。
func (s *Source) Factors(ctx context.Context, security domain.Security, from, to string) ([]domain.PriceFactor, error) {
	data, err := s.read(ctx, map[string]any{"op": "factors", "security": security, "from": from, "to": to})
	if err != nil {
		return nil, err
	}
	var result []domain.PriceFactor
	err = json.Unmarshal(data, &result)
	return result, err
}
func (s *Source) Bars(ctx context.Context, security domain.Security, category int, from, to string) ([]domain.Bar, error) {
	data, err := s.read(ctx, map[string]any{"op": "bars", "security": security, "category": category, "from": from, "to": to})
	if err != nil {
		return nil, err
	}
	var rows []struct {
		Date                                   string  `json:"date"` // SDK 标准化后的市场日期或分钟时间文本。
		Open, High, Low, Close, Volume, Amount float64 // 开/高/低/收价为元；Volume 此处仍为股，Amount 为元，转换后才成为 domain.Bar。
	}
	if err = json.Unmarshal(data, &rows); err != nil {
		return nil, err
	}
	bars := make([]domain.Bar, 0, len(rows))
	for _, row := range rows {
		layout := "2006-01-02"
		if len(row.Date) > 10 {
			layout = "2006-01-02 15:04"
		}
		date, err := time.ParseInLocation(layout, row.Date, domain.ExchangeTime)
		if err != nil {
			return nil, fmt.Errorf("BaoStock 日期无效：%w", err)
		}
		// SDK 成交量为股，领域行情为手；只在适配边界转换一次，数据库再按约定转回股。
		bars = append(bars, domain.Bar{Date: date, Open: row.Open, High: row.High, Low: row.Low, Close: row.Close, Volume: row.Volume / 100, Amount: row.Amount})
	}
	if len(bars) == 0 {
		return nil, fmt.Errorf("BaoStock 在该时段没有可用行情，已有归档保持不变")
	}
	return bars, domain.ValidateBars(bars)
}
