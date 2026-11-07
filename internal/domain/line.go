package domain

import "time"

// HostStatus 是单端点的一次探测结果；具体可用性与耗时口径由所属 LineReport.Probe 决定。
type HostStatus struct {
	Name    string `json:"name"`            // HostName 展示名称，主备站重复地址优先保留主站名称。
	Address string `json:"address"`         // IP/域名:端口，与原始 Host.Address 对应。
	Latency int64  `json:"latencyMs"`       // tcp 为建连耗时；quote 为拨号、握手及完整行情响应的端到端毫秒数。
	OK      bool   `json:"ok"`              // tcp 表示建连成功；quote 表示真实行情请求成功，均不代表持续在线。
	Error   string `json:"error,omitempty"` // 该端点失败或取消的原因；成功时通常为空。
}

// LineReport 汇总配置回读与线路探测，Total、Tested、Available 使用不同口径。
type LineReport struct {
	Probe          string       `json:"probe,omitempty"` // tcp=仅建连初筛，quote=含握手与完整行情响应的往返测速；旧日志可能为空。
	CheckedAt      time.Time    `json:"checkedAt"`       // 本次探测开始的 UTC 时刻，用于区分历史检测和当前状态。
	Source         string       `json:"source"`          // 配置来源，当前为 builtin，表示使用内置模板生成。
	ConfigChecked  bool         `json:"configChecked"`   // 配置已生成并成功回读校验，不代表线路已经业务可用。
	ConfigBytes    int          `json:"configBytes"`     // 生成的 GBK/CRLF 配置字节数，并非线路下载流量。
	ConfigWarnings int          `json:"configWarnings"`  // 回读中的非致命配置提示数量，不包含线路故障数。
	Total          int          `json:"total"`           // HQHOST/TRYHQHOST 合并去重后的候选线路总数。
	Tested         int          `json:"tested"`          // 实际安排探测的候选数，目前最多 24；取消项也保留探测结果。
	Available      int          `json:"available"`       // 当前 Probe 口径下的通过数，等于 Results 中 OK=true 的数量。
	Results        []HostStatus `json:"results"`         // 实际探测结果，成功在前，同组按耗时由小到大排列。
}
