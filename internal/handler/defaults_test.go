package handler

import (
	"encoding/json"
	"net/http/httptest"
	"reflect"
	"testing"

	"quotes/internal/domain"
)

func TestMarketSelectionsAreJSONArrays(t *testing.T) {
	server := newTestServer(t, t.TempDir())
	recorder := httptest.NewRecorder()
	server.Handler().ServeHTTP(recorder, httptest.NewRequest("GET", "/api/defaults", nil))
	var defaults struct {
		Markets []int `json:"markets"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &defaults); err != nil {
		t.Fatalf("markets must be numeric arrays, not base64 byte strings: %v", err)
	}
	if !reflect.DeepEqual(defaults.Markets, []int{0, 1, 2}) {
		t.Fatalf("wrong defaults: %v", defaults.Markets)
	}
	options := domain.DownloadOptions{Markets: []int{0, 1, 2}}
	data, err := json.Marshal(options)
	if err != nil {
		t.Fatal(err)
	}
	var restored struct {
		Markets []int `json:"markets"`
	}
	if err := json.Unmarshal(data, &restored); err != nil || !reflect.DeepEqual(restored.Markets, options.Markets) {
		t.Fatalf("task markets do not round trip: %s %v", data, err)
	}
}
