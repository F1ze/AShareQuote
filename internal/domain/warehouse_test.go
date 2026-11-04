package domain

import (
	"errors"
	"testing"
)

func TestBarQueryNormalizesEndDayAndRejectsInvalidSelection(t *testing.T) {
	q := BarQuery{Exchange: "SH", Code: "600000", Period: "1m", From: "2024-02-28", To: "2024-02-29"}
	normal, err := q.Normalize()
	if err != nil || normal.Until != "2024-03-01" || normal.Limit != 200 {
		t.Fatalf("结束日和默认窗口：%+v %v", normal, err)
	}
	for _, invalid := range []BarQuery{
		{Exchange: "BAD", Code: "600000", Period: "day", From: q.From, To: q.To},
		{Exchange: "SH", Code: "600000' OR 1=1", Period: "day", From: q.From, To: q.To},
		{Exchange: "SH", Code: "600000", Period: "day", From: "2024-03-01", To: q.To},
		{Exchange: "SH", Code: "600000", Period: "day", From: q.From, To: q.To, Limit: 1001},
	} {
		if _, err := invalid.Normalize(); !errors.Is(err, ErrInvalidInput) {
			t.Fatalf("应拒绝无效查询：%+v %v", invalid, err)
		}
	}
}
