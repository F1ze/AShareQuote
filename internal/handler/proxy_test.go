package handler

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/labstack/echo/v4"

	"quotes/internal/repository"
	"quotes/internal/service"
)

func newProxyTestServer(t *testing.T) (*Server, string) {
	t.Helper()
	store := repository.NewSettingsStore(filepath.Join(t.TempDir(), "settings.json"))
	subject, err := service.NewProxyService(store)
	if err != nil {
		t.Fatal(err)
	}
	return NewServer(Dependencies{Proxy: subject}), store.Path
}

func callProxy(t *testing.T, server *Server, method, body string) (*httptest.ResponseRecorder, map[string]string) {
	t.Helper()
	request := httptest.NewRequest(method, "/api/proxy", strings.NewReader(body))
	if body != "" {
		request.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
	}
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, request)
	payload := map[string]string{}
	if response.Body.Len() > 0 {
		if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
			t.Fatalf("解析响应 %q：%v", response.Body.String(), err)
		}
	}
	return response, payload
}

func TestProxyEndpointsRoundTrip(t *testing.T) {
	server, settingsPath := newProxyTestServer(t)

	response, payload := callProxy(t, server, http.MethodGet, "")
	if response.Code != http.StatusOK {
		t.Fatalf("初始读取状态码 %d", response.Code)
	}
	if payload["proxy"] != "" {
		t.Fatalf("初始代理 %q", payload["proxy"])
	}

	response, payload = callProxy(t, server, http.MethodPost, `{"proxy":"socks5://proxy.local"}`)
	if response.Code != http.StatusOK {
		t.Fatalf("保存状态码 %d：%v", response.Code, payload)
	}
	if payload["proxy"] != "socks5://proxy.local:1080" {
		t.Fatalf("保存后返回 %q", payload["proxy"])
	}

	response, payload = callProxy(t, server, http.MethodGet, "")
	if response.Code != http.StatusOK || payload["proxy"] != "socks5://proxy.local:1080" {
		t.Fatalf("读取已保存设置：%d %v", response.Code, payload)
	}

	reloaded, err := repository.NewSettingsStore(settingsPath).Load()
	if err != nil {
		t.Fatal(err)
	}
	if reloaded.URL != "socks5://proxy.local:1080" {
		t.Fatalf("持久化内容 %q", reloaded.URL)
	}

	response, _ = callProxy(t, server, http.MethodPost, `{"proxy":"   "}`)
	if response.Code != http.StatusOK {
		t.Fatalf("清空状态码 %d", response.Code)
	}
	_, payload = callProxy(t, server, http.MethodGet, "")
	if payload["proxy"] != "" {
		t.Fatalf("清空后仍为 %q", payload["proxy"])
	}
}

func TestProxyRejectsInvalidAddress(t *testing.T) {
	server, _ := newProxyTestServer(t)
	response, payload := callProxy(t, server, http.MethodPost, `{"proxy":"ftp://proxy"}`)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("期望 400，实际 %d：%v", response.Code, payload)
	}
	if payload["error"] == "" {
		t.Fatal("缺少错误说明")
	}
}

func TestProxyRejectsUnknownFields(t *testing.T) {
	server, _ := newProxyTestServer(t)
	response, _ := callProxy(t, server, http.MethodPost, `{"proxy":"", "extra":1}`)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("期望 400，实际 %d", response.Code)
	}
}

func TestProxyDisabledWithoutService(t *testing.T) {
	server := NewServer(Dependencies{})
	response, payload := callProxy(t, server, http.MethodGet, "")
	if response.Code != http.StatusServiceUnavailable {
		t.Fatalf("期望 503，实际 %d：%v", response.Code, payload)
	}
}
