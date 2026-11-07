package service

import (
	"context"
	"time"

	"quotes/internal/domain"
)

// Archive 负责原始数据归档：Commit 合并并替换文件，Latest 为增量采集提供已存最新时间。
type Archive interface {
	Prepare() error                                             // 准备输出目录；文件系统细节只在仓库实现。
	State(context.Context, string) (domain.ArchiveState, error) // 当前内容哈希、文件大小和因子附件哈希。
	Verify(context.Context, domain.Checkpoint) (bool, error)    // 按原断点校验完整资源；失效时交给服务重取。

	// Commit 校验并合并整个批次，返回已保存文件的回执；失败不得用半文件覆盖原文件。
	Commit(context.Context, domain.Batch) (domain.Receipt, error)
	// Latest 返回已有有效归档的最大市场时间；不存在时返回零时间，损坏时返回错误。
	Latest(context.Context, domain.Security, int) (time.Time, error)
}

// Warehouse 仅要求幂等入库，重复处理同一归档不得产生重复行情。
// 查询、名录和因子等可选能力独立声明，CSV-only 用例无需依赖它们。
type Warehouse interface {
	// Ingest 根据归档回执幂等入库，返回成功后用例才可记录本项的完成断点。
	Ingest(context.Context, domain.Batch, domain.Receipt) error
}

// LocalSource 将目录扫描与内容读取分开，便于先持久化稳定计划，再逐文件处理和恢复。
type LocalSource interface {
	// Discover 扫描目录并返回稳定的输入项列表，不在此阶段归档或入库。
	Discover(context.Context, string) ([]domain.Input, error)
	// Read 解码单文件；最后一个参数声明外部 CSV 的 shares/lots 单位，返回值统一为 Bar 的手。
	Read(context.Context, domain.Input, string) ([]domain.Bar, error)
}

// HistorySource 提供历史名录、指定日期范围的原始行情和交易日历。
// 具体来源决定可得范围；有返回记录不等于已证明区间完整，也不能忽略来源返回的错误。
type HistorySource interface {
	// Universe 按数字市场编号获取来源可得名录，应保留来源提供的退市证券和参考日期。
	Universe(context.Context, []int) ([]domain.Security, error)
	// Bars 的两个日期为包含首尾的 YYYY-MM-DD；返回统一单位、时间和未复权价的行情。
	Bars(context.Context, domain.Security, int, string, string) ([]domain.Bar, error)
	// Calendar 获取包含首尾的日期范围内的市场日历；开市不等于个股未停牌。
	Calendar(context.Context, string, string) ([]domain.TradingDay, error)
}

// SourceSpec 在组装阶段声明来源能力，由任务入口统一拒绝不支持的市场与周期组合。
type SourceSpec struct {
	Key        string        // 请求使用的唯一注册键，如 baostock；tdx 由专用流程保留，不能注册覆盖。
	Label      string        // 用户可读来源名称，用于不支持市场/周期时的错误说明。
	Markets    []int         // 该适配器支持的数字市场编号集合，必须显式声明。
	Categories []int         // 支持的协议周期编号集合，未列入的组合在创建任务前拒绝。
	Adapter    HistorySource // 实际来源实现；注册后供运行期只读使用，不能为 nil。
}

// JobRepository 保存执行事件和稳定计划；服务不依赖 JSONL 格式或目录结构。
type JobRepository interface {
	Append(string, domain.JobEvent) error  // 单事件持久提交，返回成功后才更新成功计数。
	Load(string) (domain.SavedRun, error)  // 重建某次执行，不能自动发起采集。
	Recent(int) ([]domain.SavedRun, error) // 倒序返回有限份最近执行记录。
}

// FactorRepository 只负责因子文件读写，是否获取和何时计为成功由服务决定。
type FactorRepository interface {
	LoadGBBQ(context.Context, string) (map[string][]domain.AdjustmentEvent, error)
	SaveAdjustment(context.Context, string, domain.Security, []domain.AdjustmentEvent) error
	SaveProviderFactors(context.Context, string, domain.Security, []domain.PriceFactor) error
}

// LineProvider 提供通过 TCP 连通性检测的线路；服务不依赖内置配置或 TCP 探测实现。
type LineProvider interface {
	Available(context.Context, time.Duration) ([]domain.Host, domain.LineReport, error)
}
