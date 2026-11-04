package domain

import (
	"testing"
)

func TestAShareExchangeClassification(t *testing.T) {
	for _, s := range []Security{{Market: 0, Code: "000001"}, {Market: 0, Code: "301001"}, {Market: 1, Code: "688001"}, {Market: 2, Code: "920001"}, {Market: 2, Code: "830001"}} {
		if !IsAShare(s) {
			t.Fatalf("A-share excluded: %+v", s)
		}
	}
	for _, s := range []Security{{Market: 1, Code: "000001"}, {Market: 0, Code: "399001"}, {Market: 1, Code: "510300"}, {Market: 1, Code: "900001"}, {Market: 0, Code: "200001"}, {Market: 1, Code: "60000x"}} {
		if IsAShare(s) {
			t.Fatalf("non A-share included: %+v", s)
		}
	}
}
