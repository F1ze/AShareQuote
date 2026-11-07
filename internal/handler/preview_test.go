package handler

import (
	"encoding/json"
	"fmt"
	"math"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"quotes/internal/domain"
	"quotes/internal/repository"
)

func TestPreviewAllHTTP(t *testing.T) {
	dir := t.TempDir()
	var csv strings.Builder
	csv.WriteString("date,open,high,low,close,volume,amount\n")
	for i := 0; i < 1501; i++ {
		fmt.Fprintf(&csv, "%s,10,11,9,10,0.95,950\n", time.Date(2020, 1, 1, 0, 0, 0, 0, domain.ExchangeTime).AddDate(0, 0, i).Format("2006-01-02"))
	}
	if err := os.WriteFile(filepath.Join(dir, "sh600000_day.csv"), []byte(csv.String()), 0600); err != nil {
		t.Fatal(err)
	}
	server := newTestServer(t, dir)
	for _, tc := range []struct {
		query         string
		status, count int
	}{
		{"all=true", 200, 1501}, {"limit=800", 200, 800}, {"all=invalid", 400, 0}, {"all=true&limit=0", 400, 0},
	} {
		t.Run(tc.query, func(t *testing.T) {
			response := httptest.NewRecorder()
			server.Handler().ServeHTTP(response, httptest.NewRequest("GET", "/api/preview?file=sh600000_day.csv&"+tc.query, nil))
			if response.Code != tc.status {
				t.Fatalf("状态 %d：%s", response.Code, response.Body.String())
			}
			if tc.status == 200 {
				var result domain.PreviewResponse
				if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil {
					t.Fatal(err)
				}
				if len(result.Bars) != tc.count || result.Total != 1501 {
					t.Fatalf("条数不符：%d/%d", len(result.Bars), result.Total)
				}
			}
		})
	}
}

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
