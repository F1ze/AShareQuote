package domain

// Batch 携带本次采集内容及审计上下文；请求范围可能大于来源实际返回的范围。
// Bars 不进入 JSON，数据库适配器从 Receipt 指向的已归档文件读取并校验内容。
type Batch struct {
	Security      Security `json:"security"`      // 当前批次唯一的市场/证券标识，可附来源提供的上市和退市资料。
	Category      int      `json:"category"`      // 周期编号，决定归档文件名、时间精度及仓库 period。
	Bars          []Bar    `json:"-"`             // 本次取得的标准未复权行情；入库时直接读取已落盘的归档，避免内存数据与归档不一致。
	Source        string   `json:"source"`        // 行情来源/处理路径，如 tdx_7709、baostock、local_history、archive_resume。
	RequestedFrom string   `json:"requestedFrom"` // 本次实际请求起始日 YYYY-MM-DD，增量时可晚于用户原始起点；旧偏移模式可为空。
	RequestedTo   string   `json:"requestedTo"`   // 本次实际请求结束日（包含），不是来源承诺已覆盖到的最晚时间。
	Warning       string   `json:"warning"`       // 历史边界、来源耗尽或本地覆盖未核验等说明，随回执入审计表。
	RunID         string   `json:"runId"`         // 产生该批次的执行编号，关联任务日志、入库回执与旧值修订。
}

// Receipt 描述合并后的归档及其内容哈希，既是入库输入，也是可恢复断点的依据。
// Rows/From/To 是文件实际覆盖，不能用请求范围代替；VolumeUnit 必须明确，禁止猜测单位。
type Receipt struct {
	Bytes      int64  `json:"bytes"`      // 完整归档文件字节数，不含因子附件和 sidecar。
	Path       string `json:"path"`       // 已提交的标准 CSV 路径，供仓库重新读取，不是临时文件路径。
	Rows       int    `json:"rows"`       // 新旧数据合并后的文件总行数，不含表头，不表示本次净新增行数。
	From       string `json:"from"`       // 文件实际最早市场时间，YYYY-MM-DD HH:MM。
	To         string `json:"to"`         // 文件实际最晚市场时间，不能从请求终点推定。
	SHA256     string `json:"sha256"`     // CSV 实际字节的 SHA-256 十六进制值；入库和恢复均核对该值。
	VolumeUnit string `json:"volumeUnit"` // 标准归档固定为 lots_100_shares，仓库据此换算为股。
}

// Input 固定本地任务的一项输入；它保存路径而非文件内容，读取时仍需校验文件。
type Input struct {
	Security Security // 从规范文件名识别的市场和证券，不从行情行内容猜代码。
	Category int      // 从扩展名/文件名周期识别的 K 线周期，如 .lc1=7、.lc5=0、.day=9。
	Path     string   // 扫描得到的输入文件路径，恢复计划保留此路径，不重新扫描扩大范围。
}

func FileName(s Security, category int) string {
	return MarketPrefix(s.Market) + s.Code + "_" + CategoryName(category) + ".csv"
}

// ArchiveState 是仓库对磁盘内容的当前观察，用于写断点前确认资源未变。
type ArchiveState struct {
	SHA256      string            // CSV 当前内容哈希。
	Bytes       int64             // CSV 当前字节数。
	Attachments map[string]string // 因子附件文件名到内容哈希的映射。
}
