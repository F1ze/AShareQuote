package domain

import "time"

// BookLevel 是公开行情的单档委托快照；五档不是十档 L2，也不包含逐笔委托序号。
type BookLevel struct {
	Price  float64 `json:"price"`  // 委托价，元；0 表示该档无有效报价。
	Volume int64   `json:"volume"` // 协议原始委托量，手，页面明确展示单位。
}

// RealtimeQuote 沿用 original/tdx_realtime.py 的 0x053E 字段与价格缩放。
type RealtimeQuote struct {
	Market    uint8       `json:"market"`    // 0 深圳、1 上海、2 北京；与 Code 共同标识证券。
	Code      string      `json:"code"`      // 服务端实际回传的证券代码，禁止用请求代码覆盖错配回包。
	Name      string      `json:"name"`      // 本地已知名录名称；未知时为空，前端显示名称待补充。
	Price     float64     `json:"price"`     // 最新成交价，元；0 表示无有效成交。
	LastClose float64     `json:"lastClose"` // 昨收价，元；为 0 时涨幅未知，不能除零。
	Open      float64     `json:"open"`      // 今日开盘价，元。
	High      float64     `json:"high"`      // 今日最高成交价，元。
	Low       float64     `json:"low"`       // 今日最低成交价，元。
	Volume    int64       `json:"volume"`    // 当日累计成交量，手，沿用参考快照协议约定。
	Amount    float64     `json:"amount"`    // 当日累计成交额，元。
	Bids      []BookLevel `json:"bids"`      // 买一到买五，按档位顺序。
	Asks      []BookLevel `json:"asks"`      // 卖一到卖五，按档位顺序。
}

// RealtimeTrade 是 0x0FC2 扩展成交明细；参考模块标为 L2，但不具备交易所逐笔序号。
// 时间通常是 3 秒聚合粒度；不能声称是每笔成交或独立逐笔委托流。
type RealtimeTrade struct {
	Time   string  `json:"time"`   // 源端交易时刻 HH:MM:SS，无交易日期，不与本地日期拼成伪时间。
	Price  float64 `json:"price"`  // 差分还原后的成交价，元。
	Volume int64   `json:"volume"` // 该记录成交量，手。
	Trades int64   `json:"trades"` // 该记录聚合的成交笔数。
	Side   uint16  `json:"side"`   // 0 主动买、1 主动卖、2 中性、5 盘后；其他值保留未知。
}

type RealtimeSnapshot struct {
	Quotes        []RealtimeQuote `json:"quotes"`        // 本轮有效报价，不包含上轮过期值。
	Trades        []RealtimeTrade `json:"trades"`        // 仅当前选中股票的有限成交窗口。
	Selected      string          `json:"selected"`      // 当前主屏证券的规范键，如 sh600036。
	ReceivedAt    time.Time       `json:"receivedAt"`    // 本机收到快照的 UTC 时间，不是交易所行情时间。
	LatencyMS     int64           `json:"latencyMs"`     // 上游快照读取耗时；缓存命中不改写该值。
	Cached        bool            `json:"cached"`        // 是否复用了短时间缓存。
	DetailStatus  string          `json:"detailStatus"`  // available/empty/unavailable/not_requested，仅表示扩展成交接口状态。
	DetailMessage string          `json:"detailMessage"` // 失败、无数据或能力边界提示，不推断账户是否有权限。
	L2Verified    bool            `json:"l2Verified"`    // 当前无十档或授权接口，恒为 false，禁止按有明细就设置 true。
}

// RealtimeWatch 只保存自选配置，不含报价；用于可选桌面组件复用主页面订阅。
type RealtimeWatch struct {
	Codes    []string `json:"codes"`    // 规范市场前缀+代码，最多20只。
	Selected string   `json:"selected"` // 当前主屏，必须在 Codes 中。
	Interval int      `json:"interval"` // 主屏轮询间隔秒；小组件会使用更低频的间隔。
}
