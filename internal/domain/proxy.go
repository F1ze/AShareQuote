package domain

import (
	"errors"
	"fmt"
	"net"
	"net/url"
	"strconv"
	"strings"
)

// ProxySettings 描述全应用统一的出站代理地址；空地址表示直连。
// 只约束业务规则（协议、主机、端口），不涉及隧道实现细节。
type ProxySettings struct {
	URL string
}

// ParseProxyURL 校验并规范化用户输入的代理地址。
// 允许 socks5、http、https 三种协议；端口缺省时按协议补全；支持可选用户名密码。
// 空白输入返回直连设置，不视为错误。
func ParseProxyURL(value string) (ProxySettings, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return ProxySettings{}, nil
	}
	if len(value) > 512 {
		return ProxySettings{}, errors.New("代理地址过长")
	}
	parsed, err := url.Parse(value)
	if err != nil {
		return ProxySettings{}, errors.New("代理地址格式无效")
	}
	scheme := strings.ToLower(parsed.Scheme)
	switch scheme {
	case "socks5", "socks5h", "http", "https":
	default:
		return ProxySettings{}, errors.New("代理协议须为 socks5、socks5h、http 或 https")
	}
	host := parsed.Hostname()
	if host == "" {
		return ProxySettings{}, errors.New("代理地址缺少主机名")
	}
	port := parsed.Port()
	if port == "" {
		switch scheme {
		case "http":
			port = "80"
		case "https":
			port = "443"
		default:
			port = "1080"
		}
	}
	number, err := strconv.Atoi(port)
	if err != nil || number < 1 || number > 65535 {
		return ProxySettings{}, fmt.Errorf("代理端口 %q 无效", port)
	}
	if parsed.Path != "" && parsed.Path != "/" {
		return ProxySettings{}, errors.New("代理地址不能包含路径")
	}
	if parsed.RawQuery != "" || parsed.Fragment != "" {
		return ProxySettings{}, errors.New("代理地址不能包含查询或片段")
	}
	if parsed.User != nil {
		if parsed.User.Username() == "" {
			return ProxySettings{}, errors.New("代理用户名不能为空")
		}
		if _, ok := parsed.User.Password(); !ok {
			return ProxySettings{}, errors.New("代理密码格式无效")
		}
	}
	normalized := url.URL{Scheme: scheme, Host: net.JoinHostPort(host, port), User: parsed.User}
	if normalized.Scheme == "socks5h" {
		// x/net 的 SOCKS5 实现把主机名交给代理解析，与 socks5h 语义一致，统一记为 socks5。
		normalized.Scheme = "socks5"
	}
	return ProxySettings{URL: normalized.String()}, nil
}
