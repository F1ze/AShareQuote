package service

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"quotes/internal/domain"
)

type previewFixture struct {
	bars       []domain.Bar
	reads      int
	loadEvents bool
}

func (f *previewFixture) ReadBars(context.Context, string) ([]domain.Bar, error) {
	f.reads++
	return f.bars, nil
}
func (f *previewFixture) ReadAdjustment(_ context.Context, _ string, load bool) ([]domain.AdjustmentEvent, bool, error) {
	f.loadEvents = load
	return []domain.AdjustmentEvent{{Date: 20240102, Type: 1, Dividend: 10, Bonus: 10}}, true, nil
}

// 验证用例可脱离文件系统运行；后复权必须使用完整序列基准，不能先截窗再计算。
func TestPreviewUsesWholeSeriesAndRejectsTraversalBeforeRead(t *testing.T) {
	f := &previewFixture{}
	for day := 1; day <= 3; day++ {
		f.bars = append(f.bars, domain.Bar{Date: time.Date(2024, 1, day, 0, 0, 0, 0, domain.ExchangeTime), Open: 10, High: 10, Low: 10, Close: 10, Volume: 1, Amount: 1000})
	}
	before := append([]domain.Bar(nil), f.bars...)
	s := NewPreviewService(func() string { return "unused" }, func(string) PreviewRepository { return f })
	_, err := s.Preview(context.Background(), domain.PreviewQuery{File: "../secret.csv", Mode: "raw"})
	if !errors.Is(err, domain.ErrInvalidInput) || f.reads != 0 {
		t.Fatalf("越界名称不能访问仓库：%v reads=%d", err, f.reads)
	}
	full, err := s.Preview(context.Background(), domain.PreviewQuery{File: "sh600000_day.csv", Mode: "hfq", Limit: 3})
	if err != nil {
		t.Fatal(err)
	}
	tail, err := s.Preview(context.Background(), domain.PreviewQuery{File: "sh600000_day.csv", Mode: "hfq", Limit: 1})
	if err != nil {
		t.Fatal(err)
	}
	if tail.Total != 3 || len(tail.Bars) != 1 || tail.Bars[0].Close != full.Bars[2].Close || !f.loadEvents {
		t.Fatalf("截窗改变复权基准：full=%+v tail=%+v", full, tail)
	}
	if !reflect.DeepEqual(before, f.bars) {
		t.Fatal("预览改写了原始行情")
	}
}

// 全量入口必须越过原 800 条窗口，同时保持后复权的同一基准及兼容旧限量请求。
func TestPreviewAllHistory(t *testing.T) {
	f := &previewFixture{}
	for i := 0; i < 1601; i++ {
		f.bars = append(f.bars, domain.Bar{Date: time.Date(2020, 1, 1, 0, 0, 0, 0, domain.ExchangeTime).AddDate(0, 0, i), Open: 10, High: 10, Low: 10, Close: 10})
	}
	s := NewPreviewService(func() string { return "unused" }, func(string) PreviewRepository { return f })
	full, err := s.Preview(context.Background(), domain.PreviewQuery{File: "sh600000_day.csv", Mode: "hfq", All: true})
	if err != nil {
		t.Fatal(err)
	}
	tail, err := s.Preview(context.Background(), domain.PreviewQuery{File: "sh600000_day.csv", Mode: "hfq", Limit: 800})
	if err != nil {
		t.Fatal(err)
	}
	if len(full.Bars) != 1601 || len(tail.Bars) != 800 || full.From != tail.From || full.Bars[1600] != tail.Bars[799] {
		t.Fatalf("完整历史或复权基准丢失：full=%d tail=%d", len(full.Bars), len(tail.Bars))
	}
}
