// Package domain 保存协议、采集、复权和 HTTP 共用的领域类型，不反向依赖任何适配层。
package domain

import (
	"fmt"
	"net"
	"strconv"
	"strings"
	"time"
)

// Host 表示一条通达信行情线路；选线时以 IP/域名和端口共同去重。
type Host struct {
	Name   string `json:"name,omitempty"` // 站点展示名称，不参与 IP/端口去重；手填地址时可为空。
	IP     string `json:"ip"`             // 服务器 IPv4、IPv6 或 DNS 地址；与展示名称 Name 独立。
	Port   int    `json:"port"`           // TCP 端口，默认行情端口为 7709。
	Weight int    `json:"-"`              // 内置配置携带的线路权重，不对外序列化；实际可用线路按 TCP 探测耗时排序。
}

func (h Host) Address() string { return net.JoinHostPort(h.IP, strconv.Itoa(h.Port)) }
func (h Host) String() string  { return h.Address() }

// ParseHost 解析 IP/域名及可选端口，未指定端口时使用 7709。
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

// Security 以市场和六位代码共同标识证券：0=深圳，1=上海，2=北京。
// 上市/退市日期缺失时保持空值，不能从已有行情最早/最晚日期反推这些参考资料。
type Security struct {
	Market          uint8  `json:"market"`                    // 市场编号：0=SZ、1=SH、2=BJ；不能仅靠相同六位代码判断同一证券。
	Code            string `json:"code"`                      // 六位数字代码，保留前导零，不存 sh/sz/bj 前缀。
	Name            string `json:"name,omitempty"`            // 来源观察到的证券名称；空值表示未提供，不代表历史各时点名称。
	ListingDate     string `json:"listingDate,omitempty"`     // 来源提供的上市日 YYYY-MM-DD；空值表示未知。
	DelistingDate   string `json:"delistingDate,omitempty"`   // 来源提供的退市日 YYYY-MM-DD；空值可能是未退市或资料缺失。
	Status          string `json:"status,omitempty"`          // listed/delisted/unknown 等来源标准化状态；不是完整历史可交易状态序列。
	ReferenceSource string `json:"referenceSource,omitempty"` // 上市/退市等参考资料来源，如 baostock；用于审计，不是行情复权标记。
}

// TradingDay 是某一市场的日历记录，是否开市与某只证券当天是否停牌是两个概念。
type TradingDay struct {
	Exchange string `json:"exchange"` // 交易所：SH/SZ/BJ，与 Security.Market 的数字编号不同。
	Date     string `json:"date"`     // 北京时间自然日 YYYY-MM-DD。
	Open     bool   `json:"open"`     // true=交易所开市；不能据此断言每只股票都有成交。
	Source   string `json:"source"`   // 日历来源，用于追踪自动同步或人工参考导入。
}

// PriceFactor 保存供应商的十进制因子字符串，保留精度及其自身语义。
// 不与通达信分红送配事件混用；事件日期也不能直接当作历史信息的实际公开时间。
type PriceFactor struct {
	Date     string `json:"date"`     // 供应商因子对应日 YYYY-MM-DD，当前来自 BaoStock 的 dividOperateDate。
	Forward  string `json:"forward"`  // 原始前复权比例因子 foreAdjustFactor，保留十进制文本。
	Backward string `json:"backward"` // 原始后复权比例因子 backAdjustFactor，不与通达信仿射系数混用。
	Event    string `json:"event"`    // 供应商事件比例 adjustFactor；不是现金分红金额。
}

// ExchangeTime 是行情使用的北京时间口径，与宿主操作系统时区无关。
var ExchangeTime = time.FixedZone("Asia/Shanghai", 8*60*60)

// Bar 始终使用未复权价，价格和成交额单位为元，时间使用市场时区，成交量为手（100 股）。
// 成交量允许小数手；DuckDB/Parquet 适配器统一换算为股，不在领域层混用两种单位。
type Bar struct {
	Date   time.Time `json:"date"`   // K 线市场时间；非分钟周期归档时归一到所属日期零点，分钟线保留时分。
	Open   float64   `json:"open"`   // 周期内开盘价，未复权人民币元，必须为正。
	High   float64   `json:"high"`   // 周期内最高价，元，应不低于开盘、收盘和最低价。
	Low    float64   `json:"low"`    // 周期内最低价，元，应不高于开盘、收盘和最高价。
	Close  float64   `json:"close"`  // 周期内收盘价，未复权人民币元，必须为正。
	Volume float64   `json:"volume"` // 成交量，手；1 手=100 股，允许 0.95 手等奇数股对应值。
	Amount float64   `json:"amount"` // 成交额，人民币元；不按价格×成交量重新推算，保留来源值。
}

// MinutePoint 表示当日分时点，不等同于包含 OHLC 的一分钟历史 K 线。
type MinutePoint struct {
	Price  float64 `json:"price"`  // 差分解码后价格，人民币元。
	Volume int64   `json:"volume"` // 分时接口返回的成交量原值；不参与标准 Bar 归档的单位转换。
}

// Transaction 保留通达信逐笔接口字段，不作为历史 K 线入库；TCP 线路探测不调用逐笔接口。
type Transaction struct {
	Time      string  `json:"time"`      // 当日成交时分 HH:MM；接口未在此模型中携带自然日。
	Price     float64 `json:"price"`     // 成交价格，人民币元。
	Volume    int64   `json:"volume"`    // 协议返回的逐笔成交量原值，不在此模型中另作单位换算。
	Number    int64   `json:"number"`    // 协议中的成交笔数字段，按原值保留。
	BuyOrSell int64   `json:"buyOrSell"` // 协议买卖方向标记；保留原始枚举，不用它推导研究信号。
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
