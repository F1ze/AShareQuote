package domain

import (
	"errors"
	"fmt"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

var (
	ErrInvalidInput = errors.New("输入无效")  // 可由不同入口转换为各自的参数错误，不绑定 HTTP 状态码。
	ErrNotFound     = errors.New("资源不存在") // 文件/参考资源缺失的统一语义。
	ErrUnavailable  = errors.New("服务不可用") // 可选仓库或能力未配置。
)

// DatasetQuery 是数据集检索条件，页码从 1 起；校验独立于 URL 或数据库驱动。
type DatasetQuery struct {
	Text   string // 名称或代码检索文本，UTF-8 字节长度最多 80。
	Period string // 周期键；空值表示所有周期。
	Page   int    // 页码，允许 1–100000，0 表示使用默认第一页。
}

func (q DatasetQuery) Normalize() (DatasetQuery, error) {
	if q.Page == 0 {
		q.Page = 1
	}
	if q.Page < 1 || q.Page > 100000 || len(q.Text) > 80 || (q.Period != "" && !SupportedPeriod(q.Period)) {
		return q, fmt.Errorf("%w：查询条件无效", ErrInvalidInput)
	}
	return q, nil
}

// BarQuery 使用用户可理解的包含首尾日期；Normalize 生成仓库使用的次日排他上界。
type BarQuery struct {
	Before   string // 可选排他时间游标，向历史分页；不改变 From/To 日期边界。
	Exchange string // SH/SZ/BJ，不使用数字市场值。
	Code     string // 六位证券代码，保留前导零。
	Period   string // day/1m/5m 等标准周期键。
	From     string // 包含的起始市场日期 YYYY-MM-DD。
	To       string // 包含的结束市场日期 YYYY-MM-DD。
	Until    string // Normalize 生成的排他上界，调用者无需自行拼接。
	Limit    int    // 预览上限 1–1000，0 默认 200，不改变实际存储量。
}

var securityCodePattern = regexp.MustCompile(`^[0-9]{6}$`)

func SupportedPeriod(period string) bool {
	switch period {
	case "day", "1m", "5m", "15m", "30m", "60m", "week", "month", "quarter", "year":
		return true
	}
	return false
}
func (q BarQuery) Normalize() (BarQuery, error) {
	if (q.Exchange != "SH" && q.Exchange != "SZ" && q.Exchange != "BJ") || !securityCodePattern.MatchString(q.Code) || !SupportedPeriod(q.Period) {
		return q, fmt.Errorf("%w：证券或周期无效", ErrInvalidInput)
	}
	from, err := time.ParseInLocation("2006-01-02", q.From, ExchangeTime)
	if err != nil {
		return q, fmt.Errorf("%w：开始日期无效", ErrInvalidInput)
	}
	to, err := time.ParseInLocation("2006-01-02", q.To, ExchangeTime)
	if err != nil || from.After(to) {
		return q, fmt.Errorf("%w：结束日期无效", ErrInvalidInput)
	}
	if q.Limit == 0 {
		q.Limit = 200
	}
	if q.Limit < 1 || q.Limit > 1000 {
		return q, fmt.Errorf("%w：预览数量须为 1–1000", ErrInvalidInput)
	}
	if q.Before != "" {
		if _, err := time.ParseInLocation("2006-01-02 15:04:05", q.Before, ExchangeTime); err != nil {
			return q, fmt.Errorf("%w：分页时间游标无效", ErrInvalidInput)
		}
	}
	q.Until = to.AddDate(0, 0, 1).Format("2006-01-02")
	return q, nil
}

// ReferenceImport 指定参考资料文件；来源为必填审计信息，不由文件名推断。
type ReferenceImport struct {
	Path   string `json:"path"`   // 本机 CSV 文件路径。
	Kind   string `json:"kind"`   // calendar 或 instruments。
	Source string `json:"source"` // 人工填写或自动同步的资料来源。
}

func (r ReferenceImport) Validate() error {
	if (r.Kind != "calendar" && r.Kind != "instruments") || filepath.Ext(r.Path) != ".csv" || strings.TrimSpace(r.Source) == "" {
		return fmt.Errorf("%w：请选择参考数据类型、CSV 文件并填写来源", ErrInvalidInput)
	}
	return nil
}

// WarehouseDataset 描述一个证券/周期的数据集，空日期保持 nil，不能推断上市边界。
type WarehouseDataset struct {
	Exchange      string  `json:"exchange"`                // 市场缩写。
	Code          string  `json:"code"`                    // 六位证券代码。
	Name          *string `json:"name"`                    // 当前观察名称，资料未提供时为 null。
	Period        string  `json:"period"`                  // 标准周期键。
	Rows          int64   `json:"rows"`                    // 已存行情条数。
	Earliest      *string `json:"earliest"`                // 实际最早市场时间。
	Latest        *string `json:"latest"`                  // 实际最晚市场时间。
	UpdatedAt     string  `json:"updatedAt"`               // 最近行情入库时刻，保留仓库时区文本。
	Warning       string  `json:"warning,omitempty"`       // 最近回执的覆盖提示。
	Status        string  `json:"status,omitempty"`        // 上市状态，非历史可交易状态序列。
	ListingDate   *string `json:"listingDate,omitempty"`   // 参考上市日期，未知为 nil。
	DelistingDate *string `json:"delistingDate,omitempty"` // 参考退市日期，未知或未退市为 nil。
}
type PeriodSummary struct {
	Period  string `json:"period"`  // 周期键。
	Rows    int64  `json:"rows"`    // 该周期记录总数。
	Symbols int64  `json:"symbols"` // 该周期已存证券数。
}
type WarehouseSummary struct {
	Engine                        string             `json:"engine"`                        // 当前存储引擎名称。
	Ready                         bool               `json:"ready"`                         // 仓库能够响应查询。
	Path                          string             `json:"path"`                          // 当前数据库路径。
	SchemaVersion                 int                `json:"schemaVersion"`                 // 已应用的数据库结构版本。
	Rows                          int64              `json:"rows"`                          // 所有周期原始行情总条数。
	Symbols                       int64              `json:"symbols"`                       // 至少存有行情的证券数。
	Earliest                      *string            `json:"earliest"`                      // 全库最早市场时间，无数据为 null。
	Latest                        *string            `json:"latest"`                        // 全库最晚市场时间，无数据为 null。
	VolumeUnit                    string             `json:"volumeUnit"`                    // 仓库成交量单位，shares。
	PriceUnit                     string             `json:"priceUnit"`                     // 价格单位，CNY。
	Adjustment                    string             `json:"adjustment"`                    // 原始价格口径，none。
	Timezone                      string             `json:"timezone"`                      // 行情市场时间口径。
	Completeness                  string             `json:"completeness"`                  // 当前固定 unverified，不从条数推断完整性。
	Revisions                     int64              `json:"revisions"`                     // 已保存的旧行情修订记录数。
	FactorEvents                  int64              `json:"factorEvents"`                  // 独立因子及 gbbq 事件合计数。
	CalendarDays                  int64              `json:"calendarDays"`                  // 市场日历记录数。
	InstrumentsWithoutListingDate int64              `json:"instrumentsWithoutListingDate"` // 缺上市日参考资料的证券数。
	Periods                       []PeriodSummary    `json:"periods"`                       // 各周期汇总。
	Datasets                      []WarehouseDataset `json:"datasets"`                      // 最近更新的有限数据集样本，不是完整目录。
}
type DatasetPage struct {
	Total    int64              `json:"total"`    // 匹配的数据集总数。
	Items    []WarehouseDataset `json:"items"`    // 当前页数据。
	Page     int                `json:"page"`     // 一基页码。
	PageSize int                `json:"pageSize"` // 每页条数，当前 50。
}

// WarehouseBar 保留仓库 Decimal 的文本精度，浏览器需要数值时自行转换显示。
type WarehouseBar struct {
	Time         string `json:"ts"`            // 北京时间市场时间文本。
	Open         string `json:"open"`          // 未复权开盘价，元，十进制文本。
	High         string `json:"high"`          // 未复权最高价，元。
	Low          string `json:"low"`           // 未复权最低价，元。
	Close        string `json:"close"`         // 未复权收盘价，元。
	VolumeShares int64  `json:"volume_shares"` // 成交量，股，与 CSV 的手区分。
	AmountCNY    string `json:"amount_cny"`    // 原始成交额，元，十进制文本。
	Source       string `json:"source"`        // 保存该行时的来源。
}
type GapCandidate struct {
	Exchange string  `json:"exchange"` // 市场。
	Code     string  `json:"code"`     // 证券代码。
	Name     *string `json:"name"`     // 参考名称，可能为空。
	Date     string  `json:"date"`     // 候选缺失的开市日期。
	Reason   string  `json:"reason"`   // candidate_missing_or_suspended，停牌也可能无行情。
}
type ExportResult struct {
	Path      string           `json:"path"`      // 主行情 Parquet 路径，兼容既有调用。
	Directory string           `json:"directory"` // 同事务全部表与 manifest 的快照目录。
	Bytes     int64            `json:"bytes"`     // 快照总字节数。
	Tables    map[string]int64 `json:"tables"`    // 表名到导出行数。
}
type ImportResult struct {
	Rows int64 `json:"rows"` // 本批成功导入的参考记录数。
}
