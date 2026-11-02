// Package core contains the domain types shared by the protocol, download,
// adjustment and HTTP layers. It deliberately has no dependencies on them.
package core

import (
	"fmt"
	"net"
	"strconv"
	"strings"
	"time"
)

// Host is one TongdaXin quote endpoint.
type Host struct {
	IP     string `json:"ip"`
	Port   int    `json:"port"`
	Weight int    `json:"-"`
}

func (h Host) Address() string { return net.JoinHostPort(h.IP, strconv.Itoa(h.Port)) }
func (h Host) String() string  { return h.Address() }

// ParseHost accepts IP/domain with an optional port and defaults to 7709.
func ParseHost(value string) (Host, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return Host{}, fmt.Errorf("线路地址不能为空")
	}
	if strings.HasPrefix(value, "[") || strings.Count(value, ":") == 1 {
		host, port, err := net.SplitHostPort(value)
		if err != nil {
			return Host{}, fmt.Errorf("无效线路 %q：%w", value, err)
		}
		number, err := strconv.Atoi(port)
		if err != nil || number < 1 || number > 65535 {
			return Host{}, fmt.Errorf("无效线路端口 %q", port)
		}
		return Host{IP: host, Port: number}, nil
	}
	return Host{IP: value, Port: 7709}, nil
}

// Security identifies an instrument in a TongdaXin market.
type Security struct {
	Market uint8  `json:"market"`
	Code   string `json:"code"`
	Name   string `json:"name,omitempty"`
}

// Bar is an unadjusted OHLCV bar.
type Bar struct {
	Date   time.Time `json:"date"`
	Open   float64   `json:"open"`
	High   float64   `json:"high"`
	Low    float64   `json:"low"`
	Close  float64   `json:"close"`
	Volume float64   `json:"volume"`
	Amount float64   `json:"amount"`
}

type MinutePoint struct {
	Price  float64 `json:"price"`
	Volume int64   `json:"volume"`
}

type Transaction struct {
	Time      string  `json:"time"`
	Price     float64 `json:"price"`
	Volume    int64   `json:"volume"`
	Number    int64   `json:"number"`
	BuyOrSell int64   `json:"buyOrSell"`
}

var marketNames = map[uint8]string{0: "sz", 1: "sh", 2: "bj"}
var categoryNames = map[int]string{0: "5m", 1: "15m", 2: "30m", 3: "60m", 4: "day", 5: "week", 6: "month", 7: "1m", 8: "1m", 9: "day", 10: "quarter", 11: "year", 12: "xrday"}

func MarketPrefix(market uint8) string {
	if prefix, ok := marketNames[market]; ok {
		return prefix
	}
	return "unknown"
}

func CategoryName(category int) string {
	if name, ok := categoryNames[category]; ok {
		return name
	}
	return strings.ReplaceAll(fmt.Sprintf("cat%d", category), "/", "_")
}
