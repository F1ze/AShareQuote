// 本文件集中定义持久化模型、表结构和版本迁移；模型字段与实际数据库结构在同一处维护。
package repository

import (
	"context"
	"fmt"
	"time"

	"gorm.io/gorm"
)

// 仓库模型是持久化映射，不进入 Domain/Service。所有表名、列名和复合主键显式定义，避免 ORM 默认命名改变现有数据结构。
// 日期/因子以文本绑定到 DATE/DECIMAL，查询时也显式返回十进制文本，不让 ORM 经过 float64。
type instrumentRecord struct {
	Exchange        string    `gorm:"column:exchange;primaryKey"`      // SH/SZ/BJ；与 Code 一同唯一标识证券。
	Code            string    `gorm:"column:code;primaryKey"`          // 六位证券代码，保留前导零。
	Name            string    `gorm:"column:name"`                     // 来源观察或人工校准的证券名称。
	ListingDate     *string   `gorm:"column:listing_date;type:DATE"`   // 上市日期，nil 表示参考资料未知。
	DelistingDate   *string   `gorm:"column:delisting_date;type:DATE"` // 退市日期，nil 表示未知或尚未退市。
	Status          string    `gorm:"column:status"`                   // listed/delisted/unknown，并非历史逐日可交易状态。
	ReferenceSource string    `gorm:"column:reference_source"`         // 上市/退市资料来源，用于追溯。
	FirstObservedAt time.Time `gorm:"column:first_observed_at"`        // 首次观察的 UTC 审计时刻，冲突更新时保留。
	LastObservedAt  time.Time `gorm:"column:last_observed_at"`         // 最近一次资料观察或人工校准时刻。
}

func (instrumentRecord) TableName() string { return "instruments" }

type calendarRecord struct {
	Exchange        string `gorm:"column:exchange;primaryKey"`       // 交易所，不代表个股是否停牌。
	Date            string `gorm:"column:date;type:DATE;primaryKey"` // 北京时间自然日 YYYY-MM-DD。
	IsOpen          bool   `gorm:"column:is_open"`                   // 交易所开市标记，false 也必须显式更新。
	ReferenceSource string `gorm:"column:reference_source"`          // 日历参考资料来源。
}

func (calendarRecord) TableName() string { return "trading_calendar" }

type providerFactorRecord struct {
	Exchange       string    `gorm:"column:exchange;primaryKey"`                 // 因子对应市场。
	Code           string    `gorm:"column:code;primaryKey"`                     // 六位证券代码。
	EventDate      string    `gorm:"column:event_date;type:DATE;primaryKey"`     // 供应商提供的因子事件日期，不推断实际公开日期。
	ForwardFactor  string    `gorm:"column:forward_factor;type:DECIMAL(30,15)"`  // 原始前复权因子十进制文本。
	BackwardFactor string    `gorm:"column:backward_factor;type:DECIMAL(30,15)"` // 原始后复权因子十进制文本。
	EventFactor    string    `gorm:"column:event_factor;type:DECIMAL(30,15)"`    // 供应商事件比例，非现金分红金额。
	Source         string    `gorm:"column:source;primaryKey"`                   // 因子来源，不与 gbbq 混用。
	FetchedAt      time.Time `gorm:"column:fetched_at"`                          // 因子本次写入的 UTC 审计时刻。
}

func (providerFactorRecord) TableName() string { return "provider_factors" }

type receiptRecord struct {
	Exchange      string    `gorm:"column:exchange;primaryKey"`        // 归档证券市场。
	Code          string    `gorm:"column:code;primaryKey"`            // 归档证券代码。
	Period        string    `gorm:"column:period;primaryKey"`          // day/1m 等标准周期键。
	SHA256        string    `gorm:"column:sha256;primaryKey"`          // 实际归档字节哈希，参与幂等键。
	Source        string    `gorm:"column:source"`                     // 本次入库处理来源。
	RequestedFrom string    `gorm:"column:requested_from"`             // 用户请求起点，可能早于来源可提供的历史。
	RequestedTo   string    `gorm:"column:requested_to"`               // 用户请求终点。
	ActualFrom    string    `gorm:"column:actual_from;type:TIMESTAMP"` // 合并归档实际最早市场时间。
	ActualTo      string    `gorm:"column:actual_to;type:TIMESTAMP"`   // 合并归档实际最晚市场时间。
	Rows          int64     `gorm:"column:rows"`                       // 整个归档通过校验的记录数。
	Warning       string    `gorm:"column:warning"`                    // 来源覆盖边界或采集警告。
	RunID         string    `gorm:"column:run_id;primaryKey"`          // 执行编号；同任务同哈希只保存一次回执。
	ImportedAt    time.Time `gorm:"column:imported_at"`                // 本次提交的 UTC 审计时刻。
}

func (receiptRecord) TableName() string { return "ingestion_receipts" }

type adjustmentRecord struct {
	Exchange    string  `gorm:"column:exchange;primaryKey"`                       // 事件对应市场。
	Code        string  `gorm:"column:code;primaryKey"`                           // 六位证券代码。
	EventDate   string  `gorm:"column:event_date;type:DATE;primaryKey"`           // 除权除息事件日期 YYYY-MM-DD。
	EventType   int     `gorm:"column:event_type;primaryKey;autoIncrement:false"` // 通达信事件类型，0 也是合法主键值。
	Dividend    float64 `gorm:"column:dividend"`                                  // 原始分红字段，保留 gbbq 来源语义。
	RightsPrice float64 `gorm:"column:rights_price"`                              // 原始配股价格字段。
	Bonus       float64 `gorm:"column:bonus"`                                     // 原始送转股比例字段。
	Rights      float64 `gorm:"column:rights"`                                    // 原始配股比例字段。
	Source      string  `gorm:"column:source"`                                    // 固定 gbbq，与供应商因子分表保存。
}

func (adjustmentRecord) TableName() string { return "adjustment_events" }

const schemaVersion = 2

// 保持既有表结构及引擎版本：旧 Python 写入的数据文件可直接打开，不做有损重建。
const schemaV1 = `
CREATE TABLE IF NOT EXISTS schema_migrations(version INTEGER PRIMARY KEY, applied_at TIMESTAMPTZ DEFAULT now());
INSERT INTO schema_migrations(version) VALUES (1) ON CONFLICT DO NOTHING;
CREATE TABLE IF NOT EXISTS instruments(
    exchange VARCHAR, code VARCHAR, name VARCHAR DEFAULT '', listing_date DATE,
    delisting_date DATE, status VARCHAR DEFAULT 'unknown', reference_source VARCHAR DEFAULT '',
    first_observed_at TIMESTAMPTZ DEFAULT now(), last_observed_at TIMESTAMPTZ DEFAULT now(),
    PRIMARY KEY(exchange,code));
CREATE TABLE IF NOT EXISTS bars(
    exchange VARCHAR, code VARCHAR, period VARCHAR, ts TIMESTAMP,
    open DECIMAL(18,6), high DECIMAL(18,6), low DECIMAL(18,6), close DECIMAL(18,6),
    volume_shares BIGINT, amount_cny DECIMAL(24,4), source VARCHAR,
    ingested_at TIMESTAMPTZ DEFAULT now(), PRIMARY KEY(exchange,code,period,ts));
CREATE TABLE IF NOT EXISTS bar_revisions(
    exchange VARCHAR, code VARCHAR, period VARCHAR, ts TIMESTAMP,
    old_open DECIMAL(18,6), old_high DECIMAL(18,6), old_low DECIMAL(18,6), old_close DECIMAL(18,6),
    old_volume_shares BIGINT, old_amount_cny DECIMAL(24,4), old_source VARCHAR,
    replaced_at TIMESTAMPTZ DEFAULT now(), run_id VARCHAR);
CREATE TABLE IF NOT EXISTS ingestion_receipts(
    exchange VARCHAR, code VARCHAR, period VARCHAR, sha256 VARCHAR, source VARCHAR,
    requested_from VARCHAR, requested_to VARCHAR, actual_from TIMESTAMP, actual_to TIMESTAMP,
    rows BIGINT, warning VARCHAR, run_id VARCHAR, imported_at TIMESTAMPTZ DEFAULT now(),
    PRIMARY KEY(exchange,code,period,sha256,run_id));
CREATE TABLE IF NOT EXISTS trading_calendar(exchange VARCHAR, date DATE, is_open BOOLEAN,
    reference_source VARCHAR, PRIMARY KEY(exchange,date));
CREATE TABLE IF NOT EXISTS adjustment_events(exchange VARCHAR,code VARCHAR,event_date DATE,
    event_type INTEGER,dividend DOUBLE,rights_price DOUBLE,bonus DOUBLE,rights DOUBLE,
    source VARCHAR,PRIMARY KEY(exchange,code,event_date,event_type));
CREATE VIEW IF NOT EXISTS daily_bars AS SELECT exchange,code,ts::DATE AS trade_date,
    open,high,low,close,volume_shares,amount_cny,source,ingested_at FROM bars WHERE period='day';
`
const schemaV2 = `CREATE TABLE IF NOT EXISTS provider_factors(exchange VARCHAR,code VARCHAR,event_date DATE,
    forward_factor DECIMAL(30,15),backward_factor DECIMAL(30,15),event_factor DECIMAL(30,15),
    source VARCHAR,fetched_at TIMESTAMPTZ DEFAULT now(),PRIMARY KEY(exchange,code,event_date,source));
    INSERT INTO schema_migrations(version) VALUES (2) ON CONFLICT DO NOTHING;`

// migrate 的流程：检查未来版本 → 在同一事务创建缺失结构 → 提交版本标记。
// 任一步失败均回滚；不允许旧应用修改由更新版本创建的数据文件。
func migrate(ctx context.Context, db *gorm.DB) error {
	return db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Exec("CREATE TABLE IF NOT EXISTS schema_migrations(version INTEGER PRIMARY KEY, applied_at TIMESTAMPTZ DEFAULT now())").Error; err != nil {
			return err
		}
		var version int
		if err := tx.Table("schema_migrations").Select("coalesce(max(version),0)").Scan(&version).Error; err != nil {
			return err
		}
		if version > schemaVersion {
			return fmt.Errorf("数据库结构版本 %d 高于程序支持的 %d，请升级程序", version, schemaVersion)
		}
		// 只执行显式、可审阅的版本迁移；禁止 AutoMigrate 自动改写历史库。
		for _, statement := range []string{schemaV1, schemaV2} {
			if err := tx.Exec(statement).Error; err != nil {
				return fmt.Errorf("迁移数据库：%w", err)
			}
		}
		return nil
	})
}
