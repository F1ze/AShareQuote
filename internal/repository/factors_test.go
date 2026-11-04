package repository

import (
	"encoding/binary"
	"math"
	"os"
	"path/filepath"
	"testing"
	"time"

	"quotes/internal/domain"
)

func testBars() []domain.Bar {
	return []domain.Bar{
		{Date: time.Date(2024, 1, 1, 0, 0, 0, 0, time.Local), Open: 10, High: 10, Low: 10, Close: 10, Volume: 100},
		{Date: time.Date(2024, 1, 2, 0, 0, 0, 0, time.Local), Open: 10, High: 10, Low: 10, Close: 10, Volume: 100},
		{Date: time.Date(2024, 1, 3, 0, 0, 0, 0, time.Local), Open: 10, High: 10, Low: 10, Close: 10, Volume: 100},
	}
}

func closePrices(bars []domain.Bar) []float64 {
	out := make([]float64, len(bars))
	for i := range bars {
		out[i] = bars[i].Close
	}
	return out
}

func assertPrices(t *testing.T, bars []domain.Bar, want []float64) {
	t.Helper()
	got := closePrices(bars)
	for i := range want {
		if math.Abs(got[i]-want[i]) > 1e-9 {
			t.Fatalf("价格 %d 错误：got %v want %v", i, got, want)
		}
	}
}

func TestAdjustBarsCompositeEvent(t *testing.T) {
	bars := testBars()
	events := []domain.AdjustmentEvent{{Date: 20240102, Type: 1, Dividend: 10, Bonus: 10}}
	qfq, err := domain.AdjustBars(bars, events, "qfq")
	if err != nil {
		t.Fatal(err)
	}
	assertPrices(t, qfq, []float64{4.5, 10, 10})
	hfq, err := domain.AdjustBars(bars, events, "hfq")
	if err != nil {
		t.Fatal(err)
	}
	assertPrices(t, hfq, []float64{10, 21, 21})
	if bars[0].Close != 10 {
		t.Fatal("复权计算修改了原始切片")
	}
}

func TestAdjustBarsIgnoresFutureAndHandlesMissingEventDay(t *testing.T) {
	bars := testBars()
	future := []domain.AdjustmentEvent{{Date: 20240104, Type: 1, Dividend: 10, Bonus: 10}}
	qfq, err := domain.AdjustBars(bars, future, "qfq")
	if err != nil {
		t.Fatal(err)
	}
	assertPrices(t, qfq, []float64{10, 10, 10})

	bars = []domain.Bar{bars[0], bars[2]}
	eventBetweenBars := []domain.AdjustmentEvent{{Date: 20240102, Type: 1, Dividend: 10, Bonus: 10}}
	qfq, err = domain.AdjustBars(bars, eventBetweenBars, "qfq")
	if err != nil {
		t.Fatal(err)
	}
	assertPrices(t, qfq, []float64{4.5, 10})
}

func TestLoadGBBQAndAdjustmentCSV(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "gbbq")
	raw := make([]byte, 4+29)
	binary.LittleEndian.PutUint32(raw[:4], 1)
	record := raw[4:]
	binary.LittleEndian.PutUint32(record[:4], 20240102)
	record[4] = 1
	record[5] = 1
	copy(record[6:12], "600000")
	for i, value := range []float32{10, 5, 2, 1} {
		binary.LittleEndian.PutUint32(record[12+i*4:16+i*4], math.Float32bits(value))
	}
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	factors, err := LoadGBBQ(path)
	if err != nil {
		t.Fatal(err)
	}
	security := domain.Security{Market: 1, Code: "600000"}
	events := factors[factorKey(1, "600000")]
	if len(events) != 1 || events[0].Dividend != 10 || events[0].RightsPrice != 5 || events[0].Bonus != 2 || events[0].Rights != 1 {
		t.Fatalf("gbbq 字段解析错误：%+v", events)
	}
	if err := WriteAdjustmentCSV(dir, security, events); err != nil {
		t.Fatal(err)
	}
	loaded, err := ReadAdjustmentCSV(filepath.Join(dir, "sh600000_gbbq.csv"))
	if err != nil || len(loaded) != 1 || loaded[0].Bonus != 2 {
		t.Fatalf("因子 CSV 往返失败：%+v %v", loaded, err)
	}
}
