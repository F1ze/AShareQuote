package handler

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"strings"
	"testing"

	"quotes/internal/domain"
	"quotes/internal/service"
)

type realtimeHTTPFixture struct{}

func (realtimeHTTPFixture) RealtimeQuotes(stocks []domain.Security) ([]domain.RealtimeQuote, error) {
	return []domain.RealtimeQuote{{Market: stocks[0].Market, Code: stocks[0].Code, Price: 10, LastClose: 9}}, nil
}
func (realtimeHTTPFixture) RealtimeTrades(domain.Security) ([]domain.RealtimeTrade, error) {
	return nil, fmt.Errorf("明细站点不可用")
}

func TestRealtimeHTTPValidatesAndPreservesL1(t *testing.T) {
	source := service.NewRealtimeService(func(context.Context) service.RealtimeSource { return realtimeHTTPFixture{} }, nil)
	for _, tc := range []struct {
		query  string
		status int
	}{{"codes=sh600036&selected=sh600036", 200}, {"codes=111024,589020&selected=111024", 200}, {"codes=sz111024&selected=sz111024", 400}, {"codes=sh600036&selected=sz000001", 400}, {"codes=../secret&selected=sh600036", 400}, {"", 400}} {
		h := NewServer(Dependencies{Realtime: source}).Handler()
		request := httptest.NewRequest("GET", "/api/realtime?"+tc.query, nil)
		response := httptest.NewRecorder()
		h.ServeHTTP(response, request)
		if response.Code != tc.status {
			t.Fatalf("%s: %d %s", tc.query, response.Code, response.Body.String())
		}
		if tc.status == 200 {
			var snapshot domain.RealtimeSnapshot
			if err := json.Unmarshal(response.Body.Bytes(), &snapshot); err != nil {
				t.Fatal(err)
			}
			if len(snapshot.Quotes) != 1 || snapshot.L2Verified || snapshot.DetailStatus != "unavailable" {
				t.Fatalf("错误降级：%+v", snapshot)
			}
		}
	}
	h := NewServer(Dependencies{}).Handler()
	response := httptest.NewRecorder()
	h.ServeHTTP(response, httptest.NewRequest("GET", "/api/realtime?codes=sh600036&selected=sh600036", nil))
	if response.Code != 503 {
		t.Fatalf("未装配时状态错误：%d", response.Code)
	}
}

func TestWidgetQuoteAndWatchHTTP(t *testing.T) {
	source := service.NewRealtimeService(func(context.Context) service.RealtimeSource { return realtimeHTTPFixture{} }, nil)
	h := NewServer(Dependencies{Realtime: source}).Handler()
	for _, tc := range []struct {
		path   string
		status int
	}{{"/api/realtime?codes=sh600036&selected=sh600036&details=false", 200}, {"/api/realtime?codes=sh600036&selected=sh600036&details=invalid", 400}, {"/api/realtime/watch", 200}} {
		response := httptest.NewRecorder()
		h.ServeHTTP(response, httptest.NewRequest("GET", tc.path, nil))
		if response.Code != tc.status {
			t.Fatalf("%s: %d %s", tc.path, response.Code, response.Body.String())
		}
		if strings.Contains(tc.path, "details=false") && !strings.Contains(response.Body.String(), "not_requested") {
			t.Fatal("轻量模式未生效")
		}
	}
	response := httptest.NewRecorder()
	request := httptest.NewRequest("POST", "/api/realtime/watch", strings.NewReader(`{"codes":["sh600036"],"selected":"sh600036","interval":3}`))
	request.Header.Set("Content-Type", "application/json")
	h.ServeHTTP(response, request)
	if response.Code != 200 {
		t.Fatalf("配置发布失败：%s", response.Body.String())
	}
	response = httptest.NewRecorder()
	h.ServeHTTP(response, httptest.NewRequest("GET", "/api/realtime/watch", nil))
	if strings.Contains(response.Body.String(), "sh600519") {
		t.Fatal("未复用主面板自选")
	}
}
