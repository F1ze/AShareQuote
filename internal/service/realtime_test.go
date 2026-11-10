package service

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"

	"quotes/internal/domain"
)

type liveFixture struct {
	calls      *atomic.Int32
	tradeCalls *atomic.Int32
	failQuotes bool
	failTrades bool
}

func (f liveFixture) RealtimeQuotes(stocks []domain.Security) ([]domain.RealtimeQuote, error) {
	f.calls.Add(1)
	if f.failQuotes {
		return nil, errors.New("quote offline")
	}
	return []domain.RealtimeQuote{{Market: stocks[0].Market, Code: stocks[0].Code, Price: 10, LastClose: 9}}, nil
}
func (f liveFixture) RealtimeTrades(domain.Security) ([]domain.RealtimeTrade, error) {
	if f.tradeCalls != nil {
		f.tradeCalls.Add(1)
	}
	if f.failTrades {
		return nil, errors.New("unsupported or offline")
	}
	return []domain.RealtimeTrade{{Time: "09:31:03", Price: 10, Volume: 95}}, nil
}

func TestRealtimeCachesAndDegradesWithoutClaimingL2(t *testing.T) {
	var calls atomic.Int32
	s := NewRealtimeService(func(context.Context) RealtimeSource { return liveFixture{calls: &calls, failTrades: true} }, nil)
	stocks, selected, err := ParseRealtimeSelection("600036,sz000001", "sh600036")
	if err != nil {
		t.Fatal(err)
	}
	first, err := s.Snapshot(context.Background(), stocks, selected)
	if err != nil || len(first.Quotes) != 1 || first.DetailStatus != "unavailable" || first.L2Verified || first.ReceivedAt.IsZero() {
		t.Fatalf("降级失败：%+v %v", first, err)
	}
	second, err := s.Snapshot(context.Background(), stocks, selected)
	if err != nil || !second.Cached || calls.Load() != 1 || !second.ReceivedAt.Equal(first.ReceivedAt) {
		t.Fatalf("缓存口径错误：%+v %v", second, err)
	}
	s = NewRealtimeService(func(context.Context) RealtimeSource { return liveFixture{calls: &calls} }, nil)
	result, err := s.Snapshot(context.Background(), stocks, selected)
	if err != nil || result.DetailStatus != "available" || result.L2Verified {
		t.Fatalf("扩展成交不等于完整 L2：%+v %v", result, err)
	}
}

func TestRealtimeRejectsInvalidSelectionsAndQuoteFailure(t *testing.T) {
	for _, tc := range [][2]string{{"", "600036"}, {"600036", "sz000001"}, {"not-a-stock", "600036"}, {"sh000001", "sh000001"}} {
		if _, _, err := ParseRealtimeSelection(tc[0], tc[1]); err == nil {
			t.Fatalf("接受无效监控：%v", tc)
		}
	}
	var calls atomic.Int32
	s := NewRealtimeService(func(context.Context) RealtimeSource { return liveFixture{calls: &calls, failQuotes: true} }, nil)
	stocks, selected, _ := ParseRealtimeSelection("600036", "600036")
	if _, err := s.Snapshot(context.Background(), stocks, selected); err == nil {
		t.Fatal("快照失败被当作正常数据")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := s.Snapshot(ctx, stocks, selected); err == nil {
		t.Fatal("忽略取消")
	}
}

func TestWidgetQuotesSkipTradesAndShareWatch(t *testing.T) {
	var calls, tradeCalls atomic.Int32
	source := NewRealtimeService(func(context.Context) RealtimeSource { return liveFixture{calls: &calls, tradeCalls: &tradeCalls} }, nil)
	stocks, selected, _ := ParseRealtimeSelection("sh600036,sz000001", "sh600036")
	result, err := source.Quotes(context.Background(), stocks, selected)
	if err != nil || result.DetailStatus != "not_requested" || len(result.Trades) != 0 || tradeCalls.Load() != 0 {
		t.Fatalf("轻量模式发起了明细：%+v %v", result, err)
	}
	watch := domain.RealtimeWatch{Codes: []string{"sz000001", "sh600036"}, Selected: "sz000001", Interval: 5}
	if err := source.SetWatch(watch); err != nil {
		t.Fatal(err)
	}
	watch.Codes[0] = "sh600519"
	read := source.Watch()
	if read.Codes[0] != "sz000001" || read.Selected != "sz000001" {
		t.Fatalf("主面板自选被修改：%+v", read)
	}
	read.Codes[0] = "sh600519"
	if source.Watch().Codes[0] != "sz000001" {
		t.Fatal("共享了可变切片")
	}
	if err := source.SetWatch(domain.RealtimeWatch{Codes: []string{"sh600036"}, Selected: "sz000001", Interval: 1}); err == nil {
		t.Fatal("接受非法自选")
	}
}
