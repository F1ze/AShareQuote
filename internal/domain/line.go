package domain

import "time"

// HostStatus 是单端点的一次 TCP 连通性结果，不代表行情业务可用或持续在线。
type HostStatus struct {
	Name    string `json:"name"`            // HostName 展示名称，主备站重复地址优先保留主站名称。
	Address string `json:"address"`         // IP/域名:端口，与原始 Host.Address 对应。
	Latency int64  `json:"latencyMs"`       // TCP/代理隧道建立耗时，毫秒；不含排队、限流、握手或业务请求。
	OK      bool   `json:"ok"`              // 建立 TCP 连接且上下文未取消；探测成功后立即关闭连接。
	Error   string `json:"error,omitempty"` // 该端点失败或取消的原因；成功时通常为空。
}

// LineReport 汇总配置回读与 TCP 探测，Total、Tested、Available 使用不同口径。
type LineReport struct {
	Probe          string       `json:"probe,omitempty"` // 新报告为 tcp；旧日志可能为空，其耗时仍按历史检测口径解释。
	CheckedAt      time.Time    `json:"checkedAt"`       // 本次探测开始的 UTC 时刻，用于区分历史检测和当前状态。
	Source         string       `json:"source"`          // 配置来源，当前为 builtin，表示使用内置模板生成。
	ConfigChecked  bool         `json:"configChecked"`   // 配置已生成并成功回读校验，不代表线路已经业务可用。
	ConfigBytes    int          `json:"configBytes"`     // 生成的 GBK/CRLF 配置字节数，并非线路下载流量。
	ConfigWarnings int          `json:"configWarnings"`  // 回读中的非致命配置提示数量，不包含线路故障数。
	Total          int          `json:"total"`           // HQHOST/TRYHQHOST 合并去重后的候选线路总数。
	Tested         int          `json:"tested"`          // 实际安排探测的候选数，目前最多 24；取消项也保留探测结果。
	Available      int          `json:"available"`       // TCP 探测通过数，等于 Results 中 OK=true 的数量。
	Results        []HostStatus `json:"results"`         // 实际探测结果，成功在前，同组按耗时由小到大排列。
}
