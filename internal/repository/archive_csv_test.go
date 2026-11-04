package repository

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"quotes/internal/domain"
)

func sample(day int, close float64) domain.Bar {
	return domain.Bar{Date: time.Date(2024, 1, day, 0, 0, 0, 0, ExchangeTime), Open: 10, High: 15, Low: 9, Close: close, Volume: .95, Amount: 100}
}
func TestArchiveMergesAndProtectsHistory(t *testing.T) {
	a := &CSVArchive{Directory: t.TempDir()}
	b := domain.Batch{Security: domain.Security{Market: 1, Code: "600000"}, Category: 9, Bars: []domain.Bar{sample(2, 11), sample(3, 12)}, Source: "test"}
	r, err := a.Commit(context.Background(), b)
	if err != nil {
		t.Fatal(err)
	}
	b.Bars = []domain.Bar{sample(3, 13), sample(4, 14)}
	r, err = a.Commit(context.Background(), b)
	if err != nil {
		t.Fatal(err)
	}
	bars, err := ReadCSV(r.Path)
	if err != nil || len(bars) != 3 || bars[1].Close != 13 || bars[0].Volume != .95 {
		t.Fatalf("merge or unit precision lost: %+v %v", bars, err)
	}
	if r.Rows != 3 || len(r.SHA256) != 64 {
		t.Fatalf("invalid receipt: %+v", r)
	}
	before, _ := os.ReadFile(r.Path)
	b.Bars = []domain.Bar{sample(4, 99)}
	if _, err = a.Commit(context.Background(), b); err == nil {
		t.Fatal("invalid OHLC accepted")
	}
	after, _ := os.ReadFile(r.Path)
	if string(before) != string(after) {
		t.Fatal("bad data replaced history")
	}
	b.Bars = []domain.Bar{sample(5, 12)}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err = a.Commit(ctx, b); err == nil {
		t.Fatal("cancelled commit succeeded")
	}
	after, _ = os.ReadFile(r.Path)
	if string(before) != string(after) {
		t.Fatal("cancellation replaced history")
	}
	if _, err = os.Stat(filepath.Join(a.Directory, "sh600000_day.csv.meta.json")); err != nil {
		t.Fatal(err)
	}
}
