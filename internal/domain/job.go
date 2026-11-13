package domain

import "time"

// DownloadOptions 是任务的固定配置。日期模式优先于旧版 Start/Count 偏移模式。
// Markets 使用 []int，避免 []uint8 被 JSON 编码成 Base64 字符串而破坏前后端约定。
type DownloadOptions struct {
	Markets        []int  `json:"markets"`        // 批量市场：0=深圳、1=上海、2=北京；手填 Codes 时以代码清单为准。
	Codes          string `json:"codes"`          // 可选证券清单，如 sh600000,sz000001；非空时不再下载整张市场码表。
	Category       int    `json:"category"`       // 协议周期编号，如 9=日线、7=1分钟、0=5分钟；完整映射见 domain.categoryNames。
	Start          int    `json:"start"`          // 旧版偏移模式：跳过最近多少根 K 线，0 为最新；日期模式会重置为 0。
	Count          int    `json:"count"`          // 旧版偏移模式的任务总条数，默认 800；日期模式不以它限制总条数。
	StartDate      string `json:"startDate"`      // 日期模式起点，YYYY-MM-DD，北京时间，包含当天；增量模式也必须提供。
	EndDate        string `json:"endDate"`        // 日期模式终点，YYYY-MM-DD，包含当天全部分钟；不得晚于北京时间今天。
	Workers        int    `json:"workers"`        // 通达信并发 worker 数，0 使用默认 32，允许 1–64；SDK 来源不按此值并发。
	RequestRate    int    `json:"requestRate"`    // 所有通达信连接共享的每秒请求启动上限，0 默认 40，允许 1–200。
	Timeout        int    `json:"timeout"`        // 通达信拨号/读写超时秒数，0 使用默认 8，允许 2–60；不是整项任务总时限。
	OutputDir      string `json:"outputDir"`      // 原始 CSV 和因子附件的目录；空值用 JobService 默认目录，启动时转绝对路径。
	GBBQPath       string `json:"gbbqPath"`       // 可选通达信 gbbq 文件路径；显式指定却无法读取时任务失败。
	ASharesOnly    bool   `json:"aSharesOnly"`    // 按市场筛选 A 股；通达信手填清单同时支持 ETF 和可转债。
	Incremental    bool   `json:"incremental"`    // 从已有归档最新日回看 7 个自然日重取，合并时保留更早历史；不自动补齐远期空洞。
	Storage        string `json:"storage"`        // duckdb=归档后入库；csv 或空值=仅归档，数据库是 CSV 之外的附加存储。
	ImportDir      string `json:"importDir"`      // 非空时走本地历史导入流程；空值时从网络来源采集。
	CSVVolumeUnit  string `json:"csvVolumeUnit"`  // 外部 CSV 的成交量单位：shares=股、lots=手；本地导入必须显式选择，不猜单位。
	Source         string `json:"source"`         // 网络来源注册键，空值为 tdx；例如 baostock，其支持范围由 SourceSpec 声明。
	CollectFactors bool   `json:"collectFactors"` // 历史源采集时同步独立比例因子；要求来源实现 Factors，可选仓库实现 StoreFactors。
}

type LogEntry struct {
	Time    string `json:"time"`    // 展示用宿主本地时分秒 HH:MM:SS，不是行情时间或持久化事件编号。
	Level   string `json:"level"`   // info/warn/error，供页面区分普通进度、覆盖提示和处理错误。
	Message string `json:"message"` // 已格式化的中文业务说明，不让页面再解析协议错误码。
}

// FileResult 描述保存后的整个归档文件，Bars/From/To 包含本次合并前已有的历史。
type FileResult struct {
	Category  int    `json:"category"`          // 此文件实际周期；本地一次导入可能混有日线和多种分钟线。
	Name      string `json:"name"`              // 不带目录的归档名，如 sh600000_day.csv；恢复时禁止包含路径。
	Code      string `json:"code"`              // 六位证券代码，市场由 Market 单独表示。
	Market    string `json:"market"`            // 展示用 SH/SZ/BJ，与 DownloadOptions 中的数字市场值不同。
	Bars      int    `json:"bars"`              // 合并后整个 CSV 的记录数，不是本次新增数量。
	Size      int64  `json:"size"`              // 保存后 CSV 的磁盘字节数，不含 sidecar 和因子附件。
	Duration  int64  `json:"durationMs"`        // 本次证券处理耗时，毫秒；恢复复用时沿用原文件结果。
	CreatedAt string `json:"createdAt"`         // 展示用保存完成时分秒，宿主本地时间，不代表归档所覆盖的日期。
	From      string `json:"from"`              // 文件实际最早市场时间，YYYY-MM-DD HH:MM，可能早于本次请求起点。
	To        string `json:"to"`                // 文件实际最晚市场时间，不能用请求结束日替代。
	Warning   string `json:"warning,omitempty"` // 来源耗尽、协议边界或附件等提示；文件已保存也可能带覆盖警告。
}

// TaskSnapshot 是供 HTTP、SSE 和日志使用的状态副本。
// Done 包含成功与失败，Bars 统计本次成功处理的读取量，Skipped 统计复用的有效断点。
type TaskSnapshot struct {
	LineReport *LineReport     `json:"lineReport,omitempty"` // 本任务实际执行的线路自检报告；离线/其他来源及旧日志可为空。
	ID         string          `json:"id"`                   // 本次执行编号 YYYYMMDD-HHMMSS.纳秒；恢复会新建编号，不覆盖原任务日志。
	State      string          `json:"state"`                // 状态及合法迁移见领域任务状态及 service/job_run.go 的说明；interrupted 表示进程重启后的中断任务。
	Total      int             `json:"total"`                // 计划处理项数：网络任务按证券计，本地任务按输入文件计，准备阶段可能为 0。
	Done       int             `json:"done"`                 // 已记录结果的项数=Succeeded+Failed；取消中的未完成项不计入。
	Succeeded  int             `json:"succeeded"`            // 处理成功项数，包含复用有效断点的项；不是新增证券数。
	Failed     int             `json:"failed"`               // 读取、归档、入库或断点记录失败的项数。
	Bars       int64           `json:"bars"`                 // 成功项本次读取并保留的行情量；恢复复用计 0，重叠刷新也计入，不等于新增入库行数。
	Pages      int64           `json:"pages"`                // 通达信成功请求页数（含空页）；历史源按逐证券处理尝试计数（含失败），并非 SDK 实际页数；本地不增加。
	Warnings   int             `json:"warnings"`             // 带非空 Warning 的成功项数，不是提示文本条数，提示不代表该项保存失败。
	Skipped    int             `json:"skipped"`              // 校验通过且成功复用断点的项数，已包含在 Succeeded 中。
	Percent    float64         `json:"percent"`              // Done/Total*100，单位百分比；100 仅代表处理结束，不代表历史完整。
	Speed      float64         `json:"speed"`                // Done/Elapsed，单位项/秒；按证券或文件计，不是网络下载字节速度。
	Elapsed    float64         `json:"elapsed"`              // 自任务创建起的秒数，含准备阶段；有 EndedAt 后停止增长。
	Message    string          `json:"message"`              // 当前阶段或最终结果的中文说明。
	OutputDir  string          `json:"outputDir"`            // 当前任务原始归档目录的绝对路径，与数据库所在 data-dir 不同。
	StartedAt  time.Time       `json:"startedAt"`            // 任务创建时刻，JSON 携带时区；不是第一根行情日期。
	EndedAt    *time.Time      `json:"endedAt,omitempty"`    // 终态时间；运行时为 nil，重启识别的中断任务可能显示恢复观察时刻。
	Options    DownloadOptions `json:"options"`              // 默认值已补齐的固定选项，作为日志和后续恢复的输入。
	Recent     []FileResult    `json:"recent"`               // 最近至多 10 个成功文件，新结果在前；不是任务的完整文件清单。
	Logs       []LogEntry      `json:"logs"`                 // 最近至多 120 条内存业务日志，按写入顺序排列；不是持久断点事件流。
}

// Checkpoint 记录一组已经保存成功的资源；恢复资格取决于内容哈希，不只看文件是否存在。
type Checkpoint struct {
	File        FileResult        `json:"file"`                  // 成功保存的整个行情文件描述，含周期、实际范围和总条数。
	SHA256      string            `json:"sha256"`                // 该 CSV 保存时的字节哈希，恢复时必须与磁盘内容一致。
	Read        int               `json:"read"`                  // 本次成功项读取量，用于日志重放统计；不能用 File.Bars 的合并总量替代。
	Skipped     bool              `json:"skipped"`               // true 表示此结果来自有效断点复用，用于重建 Skipped 统计。
	Attachments map[string]string `json:"attachments,omitempty"` // 附件文件名→SHA-256，含当时存在的 gbbq/BaoStock 因子；不记录目录。
}

// JobEvent 是 JSONL 中的一行事件；字段是可选载荷，不同 Kind 只填对应内容。
// start/planned/finish 携带快照，planned 还携带计划；result 携带一项成功断点。
type JobEvent struct {
	Kind      string         `json:"kind"`                // start=起始、planned=计划已固定、result=单项成功、finish=终态。
	Snapshot  *TaskSnapshot  `json:"snapshot,omitempty"`  // 统计与选项基线；读取时替换此前快照，result 在此基础上累加。
	Completed *Checkpoint    `json:"completed,omitempty"` // 单项完成资源，nil 表示该事件不新增成功断点。
	Plan      *IngestionPlan `json:"plan,omitempty"`      // 当次证券/文件计划，只有确定清单后才记录，不能从当前行情源反推。
}

// IngestionPlan 固定任务当时的证券或本地文件清单。
// 恢复时沿用该清单，避免上市、退市或目录变化悄悄改变原任务的处理范围。
type IngestionPlan struct {
	Securities []Security `json:"securities,omitempty"` // 网络任务的证券清单，周期沿用任务 Options.Category。
	Inputs     []Input    `json:"inputs,omitempty"`     // 本地任务的输入清单，每项自带周期和路径，允许一次导入多种周期。
}

// SavedRun 是一次日志重放的内存结果，不是正在运行的协程，也不会自动继续下载。
type SavedRun struct {
	Snapshot  TaskSnapshot          // 按事件顺序重建的最近状态；缺少 finish 时可能仍是旧进程的活动状态。
	Completed map[string]Checkpoint // 文件名→成功断点，作为恢复候选；实际复用仍须验证全部哈希。
	Plan      *IngestionPlan        // 最后记录的原任务计划；nil 表示任务尚未完成计划准备。
}
