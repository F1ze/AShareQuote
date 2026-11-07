package handler

import (
	"errors"
	"net/http"

	"github.com/labstack/echo/v4"
)

// proxyPayload 是 /api/proxy 的请求与响应体；proxy 为空表示直连。
type proxyPayload struct {
	Proxy string `json:"proxy"`
}

// handleProxyGet 返回当前生效的代理设置，供前端初始化界面状态。
func (s *Server) handleProxyGet(c echo.Context) error {
	if s.proxy == nil {
		return writeError(c, http.StatusServiceUnavailable, errors.New("代理设置服务未启用"))
	}
	return c.JSON(http.StatusOK, proxyPayload{Proxy: s.proxy.Current().URL})
}

// handleProxySet 校验并保存代理设置；持久化成功即对之后的新连接生效。
func (s *Server) handleProxySet(c echo.Context) error {
	if s.proxy == nil {
		return writeError(c, http.StatusServiceUnavailable, errors.New("代理设置服务未启用"))
	}
	var request proxyPayload
	if err := decodeJSON(c, &request); err != nil {
		return writeError(c, http.StatusBadRequest, err)
	}
	settings, err := s.proxy.Update(request.Proxy)
	if err != nil {
		return writeError(c, http.StatusBadRequest, err)
	}
	return c.JSON(http.StatusOK, proxyPayload{Proxy: settings.URL})
}
