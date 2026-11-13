package domain

import (
	"fmt"
	"reflect"
	"testing"
	"time"
)

func TestParseCodes(t *testing.T) {
	codes, err := ParseCodes("600000，sz000001；bj430047, 600000,111024,589020\n000002、920002")
	if err != nil {
		t.Fatal(err)
	}
	got := make([]string, len(codes))
	for i, code := range codes {
		got[i] = MarketPrefix(code.Market) + code.Code
	}
	want := []string{"sz000001", "sz000002", "sh111024", "sh589020", "sh600000", "bj430047", "bj920002"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("代码识别错误：got %v want %v", got, want)
	}
}

func TestTDXExplicitCodesWithASharesOnly(t *testing.T) {
	for _, tc := range []struct {
		code  string
		valid bool
	}{
		{"302132", true}, {"sz004001", true}, {"sz001000", true},
		{"111024", true}, {"589020", true}, {"sz123001", true}, {"159915", true},
		{"sz111024", false}, {"sz589020", false}, {"sh519001", false}, {"159900", false},
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
		rate, workers int
		valid         bool
	}{
		{0, 0, true}, {40, 64, true}, {1, 1, true}, {4, 12, true},
		{-1, 1, false}, {201, 1, false}, {40, 65, false},
	} {
		t.Run(fmt.Sprintf("%d_%d", tc.rate, tc.workers), func(t *testing.T) {
			o, err := NormalizeDownloadOptions(DownloadOptions{Codes: "600000", RequestRate: tc.rate, Workers: tc.workers}, "unused", time.Now())
			if (err == nil) != tc.valid {
				t.Fatalf("限流参数校验错误：%+v %v", o, err)
			}
			if tc.rate == 0 && tc.valid && (o.RequestRate != 40 || o.Workers != 32) {
				t.Fatalf("旧请求未继承默认保护：%+v", o)
			}
		})
	}
}
