package domain

import "testing"

func TestParseProxyURL(t *testing.T) {
	cases := []struct {
		name    string
		input   string
		want    string
		wantErr bool
	}{
		{name: "空白输入为直连", input: "   ", want: ""},
		{name: "socks5 默认端口", input: "socks5://127.0.0.1", want: "socks5://127.0.0.1:1080"},
		{name: "socks5h 规范化为 socks5", input: "socks5h://proxy.local", want: "socks5://proxy.local:1080"},
		{name: "socks5 显式端口", input: "SOCKS5://proxy.local:7890", want: "socks5://proxy.local:7890"},
		{name: "http 默认端口", input: "http://proxy.internal", want: "http://proxy.internal:80"},
		{name: "https 默认端口", input: "https://proxy.internal", want: "https://proxy.internal:443"},
		{name: "带认证", input: "socks5://user:pass@proxy:1080", want: "socks5://user:pass@proxy:1080"},
		{name: "IPv6 地址", input: "socks5://[::1]:1080", want: "socks5://[::1]:1080"},
		{name: "拒绝未知协议", input: "ssh://proxy", wantErr: true},
		{name: "拒绝缺主机", input: "http://", wantErr: true},
		{name: "拒绝路径", input: "http://proxy/path", wantErr: true},
		{name: "拒绝查询", input: "http://proxy?q=1", wantErr: true},
		{name: "拒绝非法端口", input: "socks5://proxy:70000", wantErr: true},
		{name: "拒绝空用户名", input: "socks5://:pass@proxy", wantErr: true},
	}
	for _, item := range cases {
		t.Run(item.name, func(t *testing.T) {
			got, err := ParseProxyURL(item.input)
			if item.wantErr {
				if err == nil {
					t.Fatalf("期望错误，实际得到 %q", got.URL)
				}
				return
			}
			if err != nil {
				t.Fatalf("意外错误：%v", err)
			}
			if got.URL != item.want {
				t.Fatalf("规范化结果 %q，期望 %q", got.URL, item.want)
			}
		})
	}
}
