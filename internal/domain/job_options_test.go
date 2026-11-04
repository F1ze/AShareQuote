package domain

import (
	"reflect"
	"testing"
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
