package handler

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// 可选平台能力未注入是服务不可用，不应被转换成用户输入错误。
func TestMissingWorkspaceCapabilitiesReturnUnavailable(t *testing.T) {
	s := newTestServer(t, t.TempDir())
	for _, tc := range []struct{ method, path, body string }{
		{http.MethodGet, "/api/fs/browse?kind=folder", ""},
		{http.MethodPost, "/api/fs/directories", `{"path":"","name":"history"}`},
	} {
		req := httptest.NewRequest(tc.method, tc.path, strings.NewReader(tc.body))
		req.Header.Set("Content-Type", "application/json")
		response := httptest.NewRecorder()
		s.Handler().ServeHTTP(response, req)
		if response.Code != http.StatusServiceUnavailable {
			t.Fatalf("%s: got %d, body=%s", tc.path, response.Code, response.Body.String())
		}
	}
}
