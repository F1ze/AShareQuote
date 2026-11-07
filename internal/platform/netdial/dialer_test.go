package netdial

import (
	"bufio"
	"context"
	"encoding/binary"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"quotes/internal/domain"
)

// startEcho 启动一个回显服务并返回监听地址；用于验证隧道端到端连通。
func startEcho(t *testing.T) string {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = listener.Close() })
	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			go func() {
				defer conn.Close()
				_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
				_, _ = io.Copy(conn, conn)
			}()
		}
	}()
	return listener.Addr().String()
}

// startFakeSOCKS5 模拟最小 SOCKS5 代理：握手（可选认证）→ 解析目标 → 隧道转发。
func startFakeSOCKS5(t *testing.T, username, password string) string {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = listener.Close() })
	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			go handleSOCKS5(conn, username, password)
		}
	}()
	return listener.Addr().String()
}

func handleSOCKS5(conn net.Conn, username, password string) {
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
	reader := bufio.NewReader(conn)
	header := make([]byte, 2)
	if _, err := io.ReadFull(reader, header); err != nil {
		return
	}
	methods := make([]byte, int(header[1]))
	if _, err := io.ReadFull(reader, methods); err != nil {
		return
	}
	hasNoAuth := false
	hasPassword := false
	for _, method := range methods {
		switch method {
		case 0x00:
			hasNoAuth = true
		case 0x02:
			hasPassword = true
		}
	}
	if username != "" {
		if !hasPassword {
			return
		}
		if _, err := conn.Write([]byte{0x05, 0x02}); err != nil {
			return
		}
		prefix := make([]byte, 2)
		if _, err := io.ReadFull(reader, prefix); err != nil {
			return
		}
		user := make([]byte, prefix[1])
		if _, err := io.ReadFull(reader, user); err != nil {
			return
		}
		length := make([]byte, 1)
		if _, err := io.ReadFull(reader, length); err != nil {
			return
		}
		pass := make([]byte, length[0])
		if _, err := io.ReadFull(reader, pass); err != nil {
			return
		}
		status := byte(0x01)
		if string(user) == username && string(pass) == password {
			status = 0x00
		}
		if _, err := conn.Write([]byte{0x01, status}); err != nil || status != 0x00 {
			return
		}
	} else {
		if !hasNoAuth {
			_, _ = conn.Write([]byte{0x05, 0xFF})
			return
		}
		if _, err := conn.Write([]byte{0x05, 0x00}); err != nil {
			return
		}
	}
	request := make([]byte, 4)
	if _, err := io.ReadFull(reader, request); err != nil {
		return
	}
	var host string
	switch request[3] {
	case 0x01:
		raw := make([]byte, 4)
		if _, err := io.ReadFull(reader, raw); err != nil {
			return
		}
		host = net.IP(raw).String()
	case 0x03:
		length := make([]byte, 1)
		if _, err := io.ReadFull(reader, length); err != nil {
			return
		}
		raw := make([]byte, length[0])
		if _, err := io.ReadFull(reader, raw); err != nil {
			return
		}
		host = string(raw)
	case 0x04:
		raw := make([]byte, 16)
		if _, err := io.ReadFull(reader, raw); err != nil {
			return
		}
		host = net.IP(raw).String()
	default:
		return
	}
	portBytes := make([]byte, 2)
	if _, err := io.ReadFull(reader, portBytes); err != nil {
		return
	}
	target, err := net.Dial("tcp", net.JoinHostPort(host, strconv.Itoa(int(binary.BigEndian.Uint16(portBytes)))))
	if err != nil {
		_, _ = conn.Write([]byte{0x05, 0x01, 0x00, 0x01, 0, 0, 0, 0, 0, 0})
		return
	}
	defer target.Close()
	if _, err := conn.Write([]byte{0x05, 0x00, 0x00, 0x01, 0, 0, 0, 0, 0, 0}); err != nil {
		return
	}
	_ = conn.SetDeadline(time.Time{})
	var pipe sync.WaitGroup
	pipe.Add(2)
	go func() { defer pipe.Done(); _, _ = io.Copy(target, conn) }()
	go func() { defer pipe.Done(); _, _ = io.Copy(conn, target) }()
	pipe.Wait()
}

// startFakeHTTPConnect 模拟 HTTP CONNECT 代理：回 200 后转发隧道。
func startFakeHTTPConnect(t *testing.T, username, password string, requests *[]string) string {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = listener.Close() })
	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			go handleHTTPConnect(conn, username, password, requests)
		}
	}()
	return listener.Addr().String()
}

func handleHTTPConnect(conn net.Conn, username, password string, requests *[]string) {
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
	reader := bufio.NewReader(conn)
	requestLine, err := reader.ReadString('\n')
	if err != nil {
		return
	}
	headers := make(map[string]string)
	for {
		line, err := reader.ReadString('\n')
		if err != nil || line == "\r\n" {
			break
		}
		parts := strings.SplitN(line, ":", 2)
		if len(parts) == 2 {
			headers[strings.TrimSpace(strings.ToLower(parts[0]))] = strings.TrimSpace(parts[1])
		}
	}
	if requests != nil {
		*requests = append(*requests, strings.TrimSpace(requestLine))
	}
	if username != "" {
		if !strings.HasPrefix(headers["proxy-authorization"], "Basic ") {
			_, _ = conn.Write([]byte("HTTP/1.1 407 Proxy Authentication Required\r\n\r\n"))
			return
		}
	}
	target := strings.TrimPrefix(strings.TrimSpace(requestLine), "CONNECT ")
	target = strings.TrimSuffix(target, " HTTP/1.1")
	upstream, err := net.Dial("tcp", target)
	if err != nil {
		_, _ = conn.Write([]byte("HTTP/1.1 502 Bad Gateway\r\n\r\n"))
		return
	}
	defer upstream.Close()
	if _, err := conn.Write([]byte("HTTP/1.1 200 Connection established\r\n\r\n")); err != nil {
		return
	}
	_ = conn.SetDeadline(time.Time{})
	var pipe sync.WaitGroup
	pipe.Add(2)
	go func() { defer pipe.Done(); _, _ = io.Copy(upstream, conn) }()
	go func() { defer pipe.Done(); _, _ = io.Copy(conn, upstream) }()
	pipe.Wait()
}

func mustSettings(t *testing.T, raw string) domain.ProxySettings {
	t.Helper()
	settings, err := domain.ParseProxyURL(raw)
	if err != nil {
		t.Fatalf("解析代理地址 %q：%v", raw, err)
	}
	return settings
}

func TestDialDirectWithoutProxy(t *testing.T) {
	manager, err := NewManager(domain.ProxySettings{})
	if err != nil {
		t.Fatal(err)
	}
	echo := startEcho(t)
	conn, err := manager.DialContext(context.Background(), "tcp", echo)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if _, err := conn.Write([]byte("ping")); err != nil {
		t.Fatal(err)
	}
	buffer := make([]byte, 4)
	_ = conn.SetReadDeadline(time.Now().Add(3 * time.Second))
	if _, err := io.ReadFull(conn, buffer); err != nil {
		t.Fatalf("回显失败：%v", err)
	}
	if string(buffer) != "ping" {
		t.Fatalf("回显内容 %q", buffer)
	}
}

func TestDialThroughSOCKS5(t *testing.T) {
	echo := startEcho(t)
	proxyAddr := startFakeSOCKS5(t, "", "")
	manager, err := NewManager(mustSettings(t, "socks5://"+proxyAddr))
	if err != nil {
		t.Fatal(err)
	}
	conn, err := manager.DialContext(context.Background(), "tcp", echo)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if _, err := conn.Write([]byte("hello")); err != nil {
		t.Fatal(err)
	}
	buffer := make([]byte, 5)
	_ = conn.SetReadDeadline(time.Now().Add(3 * time.Second))
	if _, err := io.ReadFull(conn, buffer); err != nil {
		t.Fatalf("经 SOCKS5 隧道回显失败：%v", err)
	}
	if string(buffer) != "hello" {
		t.Fatalf("回显内容 %q", buffer)
	}
}

func TestDialThroughSOCKS5WithAuth(t *testing.T) {
	echo := startEcho(t)
	proxyAddr := startFakeSOCKS5(t, "user", "secret")
	manager, err := NewManager(mustSettings(t, "socks5://user:secret@"+proxyAddr))
	if err != nil {
		t.Fatal(err)
	}
	conn, err := manager.DialContext(context.Background(), "tcp", echo)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if _, err := conn.Write([]byte("auth")); err != nil {
		t.Fatal(err)
	}
	buffer := make([]byte, 4)
	_ = conn.SetReadDeadline(time.Now().Add(3 * time.Second))
	if _, err := io.ReadFull(conn, buffer); err != nil {
		t.Fatalf("经认证 SOCKS5 隧道回显失败：%v", err)
	}
	if string(buffer) != "auth" {
		t.Fatalf("回显内容 %q", buffer)
	}
}

func TestDialThroughHTTPConnect(t *testing.T) {
	echo := startEcho(t)
	var requests []string
	proxyAddr := startFakeHTTPConnect(t, "", "", &requests)
	manager, err := NewManager(mustSettings(t, "http://"+proxyAddr))
	if err != nil {
		t.Fatal(err)
	}
	conn, err := manager.DialContext(context.Background(), "tcp", echo)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if _, err := conn.Write([]byte("tunnel")); err != nil {
		t.Fatal(err)
	}
	buffer := make([]byte, 6)
	_ = conn.SetReadDeadline(time.Now().Add(3 * time.Second))
	if _, err := io.ReadFull(conn, buffer); err != nil {
		t.Fatalf("经 CONNECT 隧道回显失败：%v", err)
	}
	if string(buffer) != "tunnel" {
		t.Fatalf("回显内容 %q", buffer)
	}
	if len(requests) == 0 || !strings.HasPrefix(requests[0], "CONNECT "+echo) {
		t.Fatalf("代理收到的请求行 %v", requests)
	}
}

func TestHotUpdateSwitchesBackToDirect(t *testing.T) {
	echo := startEcho(t)
	proxyAddr := startFakeSOCKS5(t, "", "")
	manager, err := NewManager(mustSettings(t, "socks5://"+proxyAddr))
	if err != nil {
		t.Fatal(err)
	}
	if manager.Current() == "" {
		t.Fatal("初始应有代理配置")
	}
	if err := manager.Update(domain.ProxySettings{}); err != nil {
		t.Fatal(err)
	}
	if manager.Current() != "" {
		t.Fatalf("清空后仍返回 %q", manager.Current())
	}
	conn, err := manager.DialContext(context.Background(), "tcp", echo)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if _, err := conn.Write([]byte("direct")); err != nil {
		t.Fatal(err)
	}
	buffer := make([]byte, 6)
	_ = conn.SetReadDeadline(time.Now().Add(3 * time.Second))
	if _, err := io.ReadFull(conn, buffer); err != nil {
		t.Fatalf("直连回显失败：%v", err)
	}
}

func TestHTTPTransportUsesTunnel(t *testing.T) {
	var requests []string
	proxyAddr := startFakeHTTPConnect(t, "", "", &requests)
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("ok"))
	}))
	defer target.Close()
	manager, err := NewManager(mustSettings(t, "http://"+proxyAddr))
	if err != nil {
		t.Fatal(err)
	}
	client := &http.Client{Transport: manager.HTTPTransport(), Timeout: 5 * time.Second}
	response, err := client.Get(target.URL)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	body, _ := io.ReadAll(response.Body)
	if string(body) != "ok" {
		t.Fatalf("响应内容 %q", body)
	}
	targetURL, _ := url.Parse(target.URL)
	if len(requests) == 0 || !strings.Contains(requests[0], targetURL.Host) {
		t.Fatalf("请求未经过代理：%v", requests)
	}
}

func TestUpdateRejectsInvalidSettings(t *testing.T) {
	manager, err := NewManager(domain.ProxySettings{})
	if err != nil {
		t.Fatal(err)
	}
	if err := manager.Update(domain.ProxySettings{URL: "ftp://bad"}); err == nil {
		t.Fatal("期望拒绝未知协议")
	}
	// 未知协议经 url.Parse 可以成功，但 buildConfig 不应接受；直连状态保持不变。
	if manager.Current() != "" {
		t.Fatalf("失败更新不应改变配置：%q", manager.Current())
	}
}
