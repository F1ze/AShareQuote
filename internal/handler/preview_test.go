package handler

import (
	"encoding/json"
	"math"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"quotes/internal/domain"
	"quotes/internal/repository"
)

func TestPreviewComputesQFQWithoutChangingRawCSV(t *testing.T) {
	dir := t.TempDir()
	raw := "date,open,high,low,close,volume,amount\n" +
		"2024-01-01,10,10,10,10,100,1000\n" +
		"2024-01-02,10,10,10,10,100,1000\n" +
		"2024-01-03,10,10,10,10,100,1000\n"
	path := filepath.Join(dir, "sh600000_day.csv")
	if err := os.WriteFile(path, []byte(raw), 0o600); err != nil {
		t.Fatal(err)
	}
	security := domain.Security{Market: 1, Code: "600000"}
	events := []domain.AdjustmentEvent{{Date: 20240102, Type: 1, Dividend: 10, Bonus: 10}}
	if err := repository.WriteAdjustmentCSV(dir, security, events); err != nil {
		t.Fatal(err)
	}

	server := newTestServer(t, dir)
	request := httptest.NewRequest("GET", "/api/preview?file=sh600000_day.csv&mode=qfq&limit=200", nil)
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, request)
	if response.Code != 200 {
		t.Fatalf("预览接口失败：%d %s", response.Code, response.Body.String())
	}
	var preview domain.PreviewResponse
	if err := json.Unmarshal(response.Body.Bytes(), &preview); err != nil {
		t.Fatal(err)
	}
	if len(preview.Bars) != 3 || math.Abs(preview.Bars[0].Close-4.5) > 1e-9 || preview.Bars[2].Close != 10 {
		t.Fatalf("前复权预览错误：%+v", preview.Bars)
	}
	unchanged, err := os.ReadFile(path)
	if err != nil || string(unchanged) != raw {
		t.Fatalf("原始 CSV 被修改：%v", err)
	}
}
