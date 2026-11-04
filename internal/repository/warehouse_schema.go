// 本文件集中定义持久化模型、表结构和版本迁移；模型字段与实际数据库结构在同一处维护。
package repository

import (
	"context"
	"fmt"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// 仓库模型是持久化映射，不进入 Domain/Service。所有表名、列名和复合主键显式定义，避免 ORM 默认命名改变现有数据结构。
// 日期/因子以文本绑定到 DATE/DECIMAL，查询时也显式返回十进制文本，不让 ORM 经过 float64。
type instrumentRecord struct {
	Exchange        string    `gorm:"column:exchange;primaryKey"`                              // SH/SZ/BJ；与 Code 一同唯一标识证券。
	Code            string    `gorm:"column:code;primaryKey"`                                  // 六位证券代码，保留前导零。
	Name            string    `gorm:"column:name;default:''"`                                  // 来源观察或人工校准的证券名称。
	ListingDate     *string   `gorm:"column:listing_date;type:DATE"`                           // 上市日期，nil 表示参考资料未知。
	DelistingDate   *string   `gorm:"column:delisting_date;type:DATE"`                         // 退市日期，nil 表示未知或尚未退市。
	Status          string    `gorm:"column:status;default:unknown"`                           // listed/delisted/unknown，并非历史逐日可交易状态。
	ReferenceSource string    `gorm:"column:reference_source;default:''"`                      // 上市/退市资料来源，用于追溯。
	FirstObservedAt time.Time `gorm:"column:first_observed_at;type:TIMESTAMPTZ;default:now()"` // 首次观察的 UTC 审计时刻，冲突更新时保留。
	LastObservedAt  time.Time `gorm:"column:last_observed_at;type:TIMESTAMPTZ;default:now()"`  // 最近一次资料观察或人工校准时刻。
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
	Exchange       string    `gorm:"column:exchange;primaryKey"`                       // 因子对应市场。
	Code           string    `gorm:"column:code;primaryKey"`                           // 六位证券代码。
	EventDate      string    `gorm:"column:event_date;type:DATE;primaryKey"`           // 供应商提供的因子事件日期，不推断实际公开日期。
	ForwardFactor  string    `gorm:"column:forward_factor;type:DECIMAL(30,15)"`        // 原始前复权因子十进制文本。
	BackwardFactor string    `gorm:"column:backward_factor;type:DECIMAL(30,15)"`       // 原始后复权因子十进制文本。
	EventFactor    string    `gorm:"column:event_factor;type:DECIMAL(30,15)"`          // 供应商事件比例，非现金分红金额。
	Source         string    `gorm:"column:source;primaryKey"`                         // 因子来源，不与 gbbq 混用。
	FetchedAt      time.Time `gorm:"column:fetched_at;type:TIMESTAMPTZ;default:now()"` // 因子本次写入的 UTC 审计时刻。
}

func (providerFactorRecord) TableName() string { return "provider_factors" }

type receiptRecord struct {
	Exchange      string    `gorm:"column:exchange;primaryKey"`                        // 归档证券市场。
	Code          string    `gorm:"column:code;primaryKey"`                            // 归档证券代码。
	Period        string    `gorm:"column:period;primaryKey"`                          // day/1m 等标准周期键。
	SHA256        string    `gorm:"column:sha256;primaryKey"`                          // 实际归档字节哈希，参与幂等键。
	Source        string    `gorm:"column:source"`                                     // 本次入库处理来源。
	RequestedFrom string    `gorm:"column:requested_from"`                             // 用户请求起点，可能早于来源可提供的历史。
	RequestedTo   string    `gorm:"column:requested_to"`                               // 用户请求终点。
	ActualFrom    string    `gorm:"column:actual_from;type:TIMESTAMP"`                 // 合并归档实际最早市场时间。
	ActualTo      string    `gorm:"column:actual_to;type:TIMESTAMP"`                   // 合并归档实际最晚市场时间。
	Rows          int64     `gorm:"column:rows"`                                       // 整个归档通过校验的记录数。
	Warning       string    `gorm:"column:warning"`                                    // 来源覆盖边界或采集警告。
	RunID         string    `gorm:"column:run_id;primaryKey"`                          // 执行编号；同任务同哈希只保存一次回执。
	ImportedAt    time.Time `gorm:"column:imported_at;type:TIMESTAMPTZ;default:now()"` // 本次提交的 UTC 审计时刻。
}

func (receiptRecord) TableName() string { return "ingestion_receipts" }

type adjustmentRecord struct {
	Exchange    string  `gorm:"column:exchange;primaryKey"`                                    // 事件对应市场。
	Code        string  `gorm:"column:code;primaryKey"`                                        // 六位证券代码。
	EventDate   string  `gorm:"column:event_date;type:DATE;primaryKey"`                        // 除权除息事件日期 YYYY-MM-DD。
	EventType   int     `gorm:"column:event_type;type:INTEGER;primaryKey;autoIncrement:false"` // 通达信事件类型，0 也是合法主键值。
	Dividend    float64 `gorm:"column:dividend"`                                               // 原始分红字段，保留 gbbq 来源语义。
	RightsPrice float64 `gorm:"column:rights_price"`                                           // 原始配股价格字段。
	Bonus       float64 `gorm:"column:bonus"`                                                  // 原始送转股比例字段。
	Rights      float64 `gorm:"column:rights"`                                                 // 原始配股比例字段。
	Source      string  `gorm:"column:source"`                                                 // 固定 gbbq，与供应商因子分表保存。
}

func (adjustmentRecord) TableName() string { return "adjustment_events" }

// barRecord 映射原始行情表；大批量归档仍由 DuckDB 集合导入，不逐条 Create。
type barRecord struct {
	Exchange     string    `gorm:"column:exchange;primaryKey"`                        // 证券市场。
	Code         string    `gorm:"column:code;primaryKey"`                            // 六位证券代码。
	Period       string    `gorm:"column:period;primaryKey"`                          // 标准周期键。
	Time         string    `gorm:"column:ts;type:TIMESTAMP;primaryKey"`               // 北京时间市场时刻，不带宿主时区。
	Open         string    `gorm:"column:open;type:DECIMAL(18,6)"`                    // 未复权开盘价，元，十进制文本。
	High         string    `gorm:"column:high;type:DECIMAL(18,6)"`                    // 未复权最高价。
	Low          string    `gorm:"column:low;type:DECIMAL(18,6)"`                     // 未复权最低价。
	Close        string    `gorm:"column:close;type:DECIMAL(18,6)"`                   // 未复权收盘价。
	VolumeShares int64     `gorm:"column:volume_shares"`                              // 成交量，股，与 CSV 的手区分。
	AmountCNY    string    `gorm:"column:amount_cny;type:DECIMAL(24,4)"`              // 原始成交额，元，十进制文本。
	Source       string    `gorm:"column:source"`                                     // 行情来源。
	IngestedAt   time.Time `gorm:"column:ingested_at;type:TIMESTAMPTZ;default:now()"` // 本行最近入库的 UTC 审计时刻。
}

func (barRecord) TableName() string { return "bars" }

// revisionRecord 保存被替换前的值，无业务主键，不能用 ORM 默认 ID 或去重删除修订。
type revisionRecord struct {
	Exchange        string    `gorm:"column:exchange"`                                   // 原行情市场。
	Code            string    `gorm:"column:code"`                                       // 原行情证券代码。
	Period          string    `gorm:"column:period"`                                     // 原行情周期。
	Time            string    `gorm:"column:ts;type:TIMESTAMP"`                          // 被修订的市场时间。
	OldOpen         string    `gorm:"column:old_open;type:DECIMAL(18,6)"`                // 修订前未复权开盘价。
	OldHigh         string    `gorm:"column:old_high;type:DECIMAL(18,6)"`                // 修订前未复权最高价。
	OldLow          string    `gorm:"column:old_low;type:DECIMAL(18,6)"`                 // 修订前未复权最低价。
	OldClose        string    `gorm:"column:old_close;type:DECIMAL(18,6)"`               // 修订前未复权收盘价。
	OldVolumeShares int64     `gorm:"column:old_volume_shares"`                          // 修订前成交量，股。
	OldAmountCNY    string    `gorm:"column:old_amount_cny;type:DECIMAL(24,4)"`          // 修订前成交额，元。
	OldSource       string    `gorm:"column:old_source"`                                 // 被替换数据的来源。
	ReplacedAt      time.Time `gorm:"column:replaced_at;type:TIMESTAMPTZ;default:now()"` // 发生替换的 UTC 审计时刻。
	RunID           string    `gorm:"column:run_id"`                                     // 引起本次修订的任务编号。
}

func (revisionRecord) TableName() string { return "bar_revisions" }

type migrationRecord struct {
	Version   int       `gorm:"column:version;type:INTEGER;primaryKey;autoIncrement:false"` // 结构版本号，显式赋值，禁止自增。
	AppliedAt time.Time `gorm:"column:applied_at;type:TIMESTAMPTZ;default:now()"`           // 该版本首次应用的 UTC 时刻。
}

func (migrationRecord) TableName() string { return "schema_migrations" }

const schemaVersion = 2

// migrate 的流程：读取当前库对象 → 准备版本表并拒绝未来版本 → 仅创建缺失的模型表/视图 → 写版本标记。
// GORM 根据模型生成 DDL；版本与整批事务仍由程序明确控制，不用 AutoMigrate 隐式修改已有表。
func migrate(ctx context.Context, db *gorm.DB) error {
	return db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		// DuckDB 的 catalog 与 schema 是不同层级；只检查当前连接所在库和 schema，不受同名附加库干扰。
		var names []string
		if err := tx.Table("information_schema.tables").Where("table_catalog=current_database() AND table_schema=current_schema()").Pluck("table_name", &names).Error; err != nil {
			return err
		}
		existing := make(map[string]bool, len(names))
		for _, name := range names {
			existing[name] = true
		}
		createMissing := func(model any) error {
			statement := &gorm.Statement{DB: tx}
			if err := statement.Parse(model); err != nil {
				return err
			}
			if existing[statement.Table] {
				return nil
			}
			if err := tx.Migrator().CreateTable(model); err != nil {
				return fmt.Errorf("创建仓库表 %s：%w", statement.Table, err)
			}
			existing[statement.Table] = true
			return nil
		}
		if err := createMissing(&migrationRecord{}); err != nil {
			return err
		}
		var versions []int
		if err := tx.Model(&migrationRecord{}).Order("version DESC").Pluck("version", &versions).Error; err != nil {
			return err
		}
		if len(versions) > 0 && versions[0] > schemaVersion {
			return fmt.Errorf("数据库结构版本 %d 高于程序支持的 %d，请升级程序", versions[0], schemaVersion)
		}
		// 对现有版本只补齐缺失对象；未来结构变更必须新增版本步骤，不能自动改变现有价格精度或主键。
		for _, model := range []any{&instrumentRecord{}, &barRecord{}, &revisionRecord{}, &receiptRecord{}, &calendarRecord{}, &adjustmentRecord{}, &providerFactorRecord{}} {
			if err := createMissing(model); err != nil {
				return err
			}
		}
		if !existing["daily_bars"] {
			daily := tx.Model(&barRecord{}).Select("exchange,code,CAST(ts AS DATE) AS trade_date,open,high,low,close,volume_shares,amount_cny,source,ingested_at").Where(&barRecord{Period: "day"})
			if err := tx.Migrator().CreateView("daily_bars", gorm.ViewOption{Query: daily}); err != nil {
				return err
			}
		}
		stamps := []migrationRecord{{Version: 1}, {Version: 2}}
		return tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&stamps).Error
	})
}
