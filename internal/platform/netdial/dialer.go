// Package netdial 收束全应用的出站连接：裸 TCP 客户端与出站 HTTP 传输共用同一拨号入口。
// 未配置代理时直连；配置后按 socks5 或 http(s) CONNECT 隧道连接，运行期可热更新，新连接立即生效。
package netdial

import (
	"bufio"
	"context"
	"crypto/tls"
	"encoding/base64"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"sync/atomic"
	"time"

	"golang.org/x/net/proxy"

	"quotes/internal/domain"
)

// DialFunc 与 net.Dialer.DialContext 同签名，便于协议客户端注入替换。
type DialFunc func(ctx context.Context, network, address string) (net.Conn, error)

// config 是一次热更新后的不可变快照；kind 为空表示直连。
type config struct {
	raw      string       // 规范化后的代理地址，空表示直连。
	kind     string       // ""、socks5、http、https。
	address  string       // 代理主机:端口。
	user     string       // 可选认证用户名。
	password string       // 可选认证密码。
	socks    proxy.Dialer // socks5 场景的已装配拨号器。
}

// Manager 持有当前代理配置并按其建立出站连接；并发安全。
type Manager struct {
	current atomic.Pointer[config]
}

// NewManager 按初始设置装配拨号器；设置来自已校验的领域约束。
func NewManager(initial domain.ProxySettings) (*Manager, error) {
	m := &Manager{}
	if err := m.Update(initial); err != nil {
		return nil, err
	}
	return m, nil
}

// Current 返回规范化后的当前代理地址；空串表示直连。
func (m *Manager) Current() string {
	if cfg := m.current.Load(); cfg != nil {
		return cfg.raw
	}
	return ""
}

// Update 校验并原子替换代理配置；只影响之后建立的新连接。
func (m *Manager) Update(settings domain.ProxySettings) error {
	cfg, err := buildConfig(settings)
	if err != nil {
		return err
	}
	m.current.Store(cfg)
	return nil
}

// DialContext 是统一出站入口：直连或经配置的代理隧道连接目标地址。
func (m *Manager) DialContext(ctx context.Context, network, address string) (net.Conn, error) {
	cfg := m.current.Load()
	if cfg == nil || cfg.kind == "" {
		var dialer net.Dialer
		return dialer.DialContext(ctx, network, address)
	}
	switch cfg.kind {
	case "socks5":
		if contextual, ok := cfg.socks.(proxy.ContextDialer); ok {
			return contextual.DialContext(ctx, network, address)
		}
		// x/net 的 socks 拨号器实现 ContextDialer；此处兜底保证签名兼容。
		return cfg.socks.Dial(network, address)
	case "http", "https":
		return dialConnect(ctx, cfg, address)
	}
	return nil, fmt.Errorf("不支持的代理类型 %q", cfg.kind)
}

// HTTPTransport 返回经同一代理配置出站的 HTTP 传输。
// Proxy 必须保持 nil：配置了代理时本管理器拨号的就是代理地址，再叠加 Proxy 会形成双重代理。
func (m *Manager) HTTPTransport() *http.Transport {
	return &http.Transport{
		Proxy:               nil,
		DialContext:         m.DialContext,
		TLSHandshakeTimeout: 10 * time.Second,
	}
}

// buildConfig 把领域设置装配为拨号快照；socks5 复用 x/net 的成熟实现。
func buildConfig(settings domain.ProxySettings) (*config, error) {
	if settings.URL == "" {
		return &config{}, nil
	}
	parsed, err := url.Parse(settings.URL)
	if err != nil {
		return nil, fmt.Errorf("代理地址无效：%w", err)
	}
	cfg := &config{raw: settings.URL, kind: parsed.Scheme, address: parsed.Host}
	switch cfg.kind {
	case "", "socks5", "http", "https":
	default:
		return nil, fmt.Errorf("不支持的代理类型 %q", cfg.kind)
	}
	if parsed.User != nil {
		cfg.user = parsed.User.Username()
		cfg.password, _ = parsed.User.Password()
	}
	if cfg.kind == "socks5" {
		var auth *proxy.Auth
		if cfg.user != "" {
			auth = &proxy.Auth{User: cfg.user, Password: cfg.password}
		}
		forward := &net.Dialer{Timeout: 15 * time.Second}
		socks, err := proxy.SOCKS5("tcp", cfg.address, auth, forward)
		if err != nil {
			return nil, fmt.Errorf("装配 SOCKS5 代理：%w", err)
		}
		cfg.socks = socks
	}
	return cfg, nil
}

// dialConnect 先拨到代理，再发送 HTTP CONNECT 建立到目标地址的隧道。
// https 代理在 CONNECT 前先与代理完成 TLS 握手；目标协议自身的 TLS 由调用方处理。
func dialConnect(ctx context.Context, cfg *config, address string) (net.Conn, error) {
	var dialer net.Dialer
	conn, err := dialer.DialContext(ctx, "tcp", cfg.address)
	if err != nil {
		return nil, fmt.Errorf("连接代理 %s：%w", cfg.address, err)
	}
	if cfg.kind == "https" {
		host, _, err := net.SplitHostPort(cfg.address)
		if err != nil {
			host = cfg.address
		}
		tlsConn := tls.Client(conn, &tls.Config{ServerName: host})
		if err := tlsConn.HandshakeContext(ctx); err != nil {
			_ = conn.Close()
			return nil, fmt.Errorf("代理 TLS 握手：%w", err)
		}
		conn = tlsConn
	}
	request := &http.Request{
		Method: http.MethodConnect,
		URL:    &url.URL{Opaque: address},
		Host:   address,
		Header: make(http.Header),
	}
	if cfg.user != "" {
		credentials := base64.StdEncoding.EncodeToString([]byte(cfg.user + ":" + cfg.password))
		request.Header.Set("Proxy-Authorization", "Basic "+credentials)
	}
	if err := request.Write(conn); err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("发送 CONNECT：%w", err)
	}
	response, err := http.ReadResponse(bufio.NewReader(conn), request)
	if err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("读取 CONNECT 应答：%w", err)
	}
	// CONNECT 应答没有正文，读取头后连接即成为透明隧道。
	if response.StatusCode != http.StatusOK {
		_ = conn.Close()
		return nil, fmt.Errorf("代理拒绝连接：%s", response.Status)
	}
	return conn, nil
}
