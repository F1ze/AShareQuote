package domain

// HostStatus 是单端点的一次业务探测结果，不是长期健康状态或仅 TCP ping 的结果。
type HostStatus struct {
	Address    string `json:"address"`              // IP/域名:端口，与原始 Host.Address 对应。
	Latency    int64  `json:"latencyMs"`            // 取得并发名额后，建连、握手及业务查询的总耗时，毫秒。
	OK         bool   `json:"ok"`                   // 上海码表非空且 600000 逐笔非空，同时上下文未取消才为 true。
	Securities int    `json:"securities,omitempty"` // 上海码表返回的证券数量，不是筛选后的 A 股数量。
	Ticks      int    `json:"ticks,omitempty"`      // 样本逐笔记录条数；探测最多请求 5 条，不是历史总量。
	Error      string `json:"error,omitempty"`      // 该端点失败或取消的原因；成功时通常为空。
}

// Report 汇总配置回读与线路业务探测，Total、Tested、Available 使用不同口径。
type LineReport struct {
	Source         string       `json:"source"`         // 配置来源，当前为 builtin，表示使用内置模板生成。
	ConfigChecked  bool         `json:"configChecked"`  // 配置已生成并成功回读校验，不代表线路已经业务可用。
	ConfigBytes    int          `json:"configBytes"`    // 生成的 GBK/CRLF 配置字节数，并非线路下载流量。
	ConfigWarnings int          `json:"configWarnings"` // 回读中的非致命配置提示数量，不包含线路故障数。
	Total          int          `json:"total"`          // HQHOST/TRYHQHOST 合并去重后的候选线路总数。
	Tested         int          `json:"tested"`         // 实际安排探测的候选数，目前最多 24；取消项也保留探测结果。
	Available      int          `json:"available"`      // 业务探测通过数，等于 Results 中 OK=true 的数量。
	Results        []HostStatus `json:"results"`        // 实际探测结果，成功在前，同组按耗时由小到大排列。
}
