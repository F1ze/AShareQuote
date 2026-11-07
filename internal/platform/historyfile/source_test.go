package historyfile

import (
	"context"
	"encoding/binary"
	"math"
	"os"
	"path/filepath"
	"testing"
)

func TestDiscoverIncludesExpandedASharesAndExcludesDepositaryReceipts(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"sz302132_day.csv", "sz004001_day.csv", "sz001001_day.csv", "sz309800_day.csv", "sh689009_day.csv", "bj889001_day.csv"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("date,open,high,low,close,volume,amount\n"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	inputs, err := New().Discover(context.Background(), dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(inputs) != 2 {
		t.Fatalf("本地扫描漏股或混入非股票：%+v", inputs)
	}
	seen := map[string]bool{}
	for _, input := range inputs {
		seen[input.Security.Code] = true
	}
	if !seen["302132"] || !seen["004001"] {
		t.Fatalf("本地扫描遗漏股票号段：%+v", inputs)
	}
}

func TestNativeDayAndMinuteUnits(t *testing.T) {
	for _, ext := range []string{"day", "lc1", "lc5"} {
		t.Run(ext, func(t *testing.T) {
			dir := t.TempDir()
			raw := make([]byte, 32)
			if ext == "day" {
				binary.LittleEndian.PutUint32(raw, 20240102)
				for i, v := range []uint32{1000, 1200, 900, 1100} {
					binary.LittleEndian.PutUint32(raw[4+i*4:], v)
				}
			} else {
				binary.LittleEndian.PutUint16(raw, uint16((2024-2004)*2048+102))
				binary.LittleEndian.PutUint16(raw[2:], 571)
				for i, v := range []float32{10, 12, 9, 11} {
					binary.LittleEndian.PutUint32(raw[4+i*4:], math.Float32bits(v))
				}
			}
			binary.LittleEndian.PutUint32(raw[20:], math.Float32bits(10000))
			binary.LittleEndian.PutUint32(raw[24:], 95)
			path := filepath.Join(dir, "sh600000."+ext)
			if err := os.WriteFile(path, raw, 0600); err != nil {
				t.Fatal(err)
			}
			s := New()
			inputs, err := s.Discover(context.Background(), dir)
			if err != nil || len(inputs) != 1 {
				t.Fatalf("discover: %v %v", inputs, err)
			}
			bars, err := s.Read(context.Background(), inputs[0], "lots")
			if err != nil || len(bars) != 1 || bars[0].Volume != .95 || bars[0].Close != 11 {
				t.Fatalf("native decoding: %+v %v", bars, err)
			}
			if ext != "day" && bars[0].Date.Format("15:04") != "09:31" {
				t.Fatal("minute timestamp lost")
			}
			if err := os.WriteFile(path, raw[:31], 0600); err != nil {
				t.Fatal(err)
			}
			if _, err = s.Read(context.Background(), inputs[0], "lots"); err == nil {
				t.Fatal("partial record accepted")
			}
		})
	}
}
