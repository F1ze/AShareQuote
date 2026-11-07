package domain

import (
	"fmt"
	"reflect"
	"testing"
	"time"
)

func TestParseCodes(t *testing.T) {
	codes, err := ParseCodes("600000，sz000001；bj430047, 600000\n000002、920002")
	if err != nil {
		t.Fatal(err)
	}
	got := make([]string, len(codes))
	for i, code := range codes {
		got[i] = MarketPrefix(code.Market) + code.Code
	}
	want := []string{"sz000001", "sz000002", "sh600000", "bj430047", "bj920002"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("代码识别错误：got %v want %v", got, want)
	}
}

func TestAShareOnlyOptionsUseFullStockRanges(t *testing.T) {
	for _, tc := range []struct {
		code  string
		valid bool
	}{
		{"302132", true}, {"sz004001", true}, {"sz001000", true},
		{"sz001001", false}, {"sz001199", false}, {"sz309800", false}, {"sh689009", false}, {"bj889001", false},
	} {
		t.Run(tc.code, func(t *testing.T) {
			_, err := NormalizeDownloadOptions(DownloadOptions{Codes: tc.code, ASharesOnly: true}, "unused", time.Date(2026, 11, 7, 0, 0, 0, 0, ExchangeTime))
			if (err == nil) != tc.valid {
				t.Fatalf("代码范围校验不符：valid=%v err=%v", tc.valid, err)
			}
		})
	}
}

func TestTrafficLimitOptions(t *testing.T) {
	for _, tc := range []struct {
		global, host, workers int
		valid                 bool
	}{
		{0, 0, 0, true}, {40, 8, 64, true}, {1, 1, 1, true}, {4, 0, 12, true},
		{-1, 1, 1, false}, {201, 8, 1, false}, {40, -1, 1, false}, {8, 9, 1, false}, {40, 8, 65, false},
	} {
		t.Run(fmt.Sprintf("%d_%d_%d", tc.global, tc.host, tc.workers), func(t *testing.T) {
			o, err := NormalizeDownloadOptions(DownloadOptions{Codes: "600000", RequestRate: tc.global, HostRequestRate: tc.host, Workers: tc.workers}, "unused", time.Now())
			if (err == nil) != tc.valid {
				t.Fatalf("限流参数校验错误：%+v %v", o, err)
			}
			if tc.global == 0 && tc.valid && (o.RequestRate != 40 || o.HostRequestRate != 8 || o.Workers != 12) {
				t.Fatalf("旧请求未继承默认保护：%+v", o)
			}
		})
	}
}
