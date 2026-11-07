// 本文件集中实现研究查询与一致性快照导出；查询范围和资源发布规则各自在对应方法中说明。
package repository

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"gorm.io/gorm"

	"quotes/internal/domain"
)

// warehouseTotals 是聚合投影，不是新的业务实体；复杂 API 模型的切片不参与 ORM 关系推断。
type warehouseTotals struct {
	Rows     int64   // 全库行情条数。
	Symbols  int64   // 至少有行情的证券数。
	Earliest *string // 实际最早市场时间，空库为 nil。
	Latest   *string // 实际最晚市场时间，空库为 nil。
}

type datasetKey struct {
	Exchange string
	Code     string
	Period   string
}

// 两个投影只承载数据集组装所需字段，nullable 列保留数据库中的空值语义。
type datasetInstrument struct {
	Exchange      string
	Code          string
	Name          *string
	Status        *string
	ListingDate   *string
	DelistingDate *string
}

type datasetReceipt struct {
	Exchange   string
	Code       string
	Period     string
	Warning    *string
	ImportedAt time.Time
	RunID      string
}

const datasetAggregateColumns = "b.exchange,b.code,b.period," +
	"count(*) AS rows,min(b.ts)::VARCHAR AS earliest,max(b.ts)::VARCHAR AS latest," +
	"max(b.ingested_at)::VARCHAR AS updated_at"

// Summary 在同一 ORM 事务内聚合统计、周期和近期数据集，避免各部分来自不同时间的快照。
func (d *DuckDB) Summary(ctx context.Context) (domain.WarehouseSummary, error) {
	return transact(ctx, d, func(tx *gorm.DB) (domain.WarehouseSummary, error) {
		s := domain.WarehouseSummary{Engine: "DuckDB", Ready: true, Path: d.Path, SchemaVersion: schemaVersion, VolumeUnit: "shares", PriceUnit: "CNY", Adjustment: "none", Timezone: "Asia/Shanghai", Completeness: "unverified", Periods: []domain.PeriodSummary{}, Datasets: []domain.WarehouseDataset{}}
		var totals warehouseTotals
		err := tx.Model(&barRecord{}).Select("count(*) AS rows,count(DISTINCT(exchange,code)) AS symbols,min(ts)::VARCHAR AS earliest,max(ts)::VARCHAR AS latest").Scan(&totals).Error
		if err != nil {
			return s, err
		}
		s.Rows = totals.Rows
		s.Symbols = totals.Symbols
		s.Earliest = totals.Earliest
		s.Latest = totals.Latest
		// 常规计数交给 ORM；各次读取仍处于同一个事务快照，统计不会互相错位。
		if err = tx.Model(&revisionRecord{}).Count(&s.Revisions).Error; err != nil {
			return s, err
		}
		if err = tx.Model(&adjustmentRecord{}).Count(&s.FactorEvents).Error; err != nil {
			return s, err
		}
		var providerEvents int64
		if err = tx.Model(&providerFactorRecord{}).Count(&providerEvents).Error; err != nil {
			return s, err
		}
		s.FactorEvents += providerEvents
		if err = tx.Model(&calendarRecord{}).Count(&s.CalendarDays).Error; err != nil {
			return s, err
		}
		if err = tx.Model(&instrumentRecord{}).Where(map[string]any{"listing_date": nil}).Count(&s.InstrumentsWithoutListingDate).Error; err != nil {
			return s, err
		}
		err = tx.Model(&barRecord{}).Select("period,count(*) AS rows,count(DISTINCT(exchange,code)) AS symbols").Group("period").Order("period").Scan(&s.Periods).Error
		if err != nil {
			return s, err
		}

		// 聚合只负责行情；证券资料和最新回执分别用模型查询后在 Go 中组装，不再嵌套窗口函数和多表 join。
		err = tx.Table("bars b").Select(datasetAggregateColumns).
			Group("b.exchange,b.code,b.period").Order("max(b.ingested_at) DESC,b.exchange,b.code,b.period").
			Limit(200).Scan(&s.Datasets).Error
		if err != nil {
			return s, err
		}
		instruments, err := loadDatasetInstruments(tx, s.Datasets)
		if err != nil {
			return s, err
		}
		receipts, err := loadLatestDatasetReceipts(tx, s.Datasets)
		if err != nil {
			return s, err
		}
		for i := range s.Datasets {
			item := &s.Datasets[i]
			item.Status = "unknown"
			instrumentKey := datasetKey{Exchange: item.Exchange, Code: item.Code}
			if instrument, ok := instruments[instrumentKey]; ok {
				item.Name = instrument.Name
				item.ListingDate = instrument.ListingDate
				item.DelistingDate = instrument.DelistingDate
				if instrument.Status != nil {
					item.Status = *instrument.Status
				}
			}
			if receipt, ok := receipts[datasetKey{Exchange: item.Exchange, Code: item.Code, Period: item.Period}]; ok && receipt.Warning != nil {
				item.Warning = *receipt.Warning
			}
		}
		return s, nil
	})
}

// datasetSelection 每次构造独立查询，避免 Count/Limit 改写同一个 ORM Statement 后影响下一次查询。
func datasetSelection(tx *gorm.DB, q domain.DatasetQuery) *gorm.DB {
	query := tx.Table("bars b").Joins("LEFT JOIN instruments i ON b.exchange=i.exchange AND b.code=i.code")
	if q.Period != "" {
		query = query.Where("b.period = ?", q.Period)
	}
	if q.Text != "" {
		pattern := "%" + q.Text + "%"
		query = query.Where("(b.code LIKE ? OR i.name LIKE ?)", pattern, pattern)
	}
	return query
}
func (d *DuckDB) Datasets(ctx context.Context, q domain.DatasetQuery) (domain.DatasetPage, error) {
	q, err := q.Normalize()
	if err != nil {
		return domain.DatasetPage{}, err
	}
	return transact(ctx, d, func(tx *gorm.DB) (domain.DatasetPage, error) {
		page := domain.DatasetPage{Page: q.Page, PageSize: 50, Items: []domain.WarehouseDataset{}}
		grouped := datasetSelection(tx, q).Select("b.exchange,b.code,b.period").Group("b.exchange,b.code,b.period")
		if err := tx.Table("(?) matched", grouped).Count(&page.Total).Error; err != nil {
			return page, err
		}
		err := datasetSelection(tx, q).Select(datasetAggregateColumns).
			Group("b.exchange,b.code,b.period").Order("b.exchange,b.code,b.period").
			Limit(page.PageSize).Offset((q.Page - 1) * page.PageSize).Scan(&page.Items).Error
		if err != nil {
			return page, err
		}
		instruments, err := loadDatasetInstruments(tx, page.Items)
		if err != nil {
			return page, err
		}
		for i := range page.Items {
			item := &page.Items[i]
			name := ""
			if instrument, ok := instruments[datasetKey{Exchange: item.Exchange, Code: item.Code}]; ok && instrument.Name != nil {
				name = *instrument.Name
			}
			item.Name = &name
		}
		return page, nil
	})
}

func loadDatasetInstruments(tx *gorm.DB, datasets []domain.WarehouseDataset) (map[datasetKey]datasetInstrument, error) {
	result := make(map[datasetKey]datasetInstrument, len(datasets))
	if len(datasets) == 0 {
		return result, nil
	}
	query := tx.Model(&instrumentRecord{})
	for i, item := range datasets {
		condition := &instrumentRecord{Exchange: item.Exchange, Code: item.Code}
		if i == 0 {
			query = query.Where(condition)
		} else {
			query = query.Or(condition)
		}
	}
	records := []datasetInstrument{}
	err := query.Select("exchange,code,name,status,listing_date::VARCHAR AS listing_date,delisting_date::VARCHAR AS delisting_date").Find(&records).Error
	for _, record := range records {
		result[datasetKey{Exchange: record.Exchange, Code: record.Code}] = record
	}
	return result, err
}

func loadLatestDatasetReceipts(tx *gorm.DB, datasets []domain.WarehouseDataset) (map[datasetKey]datasetReceipt, error) {
	result := make(map[datasetKey]datasetReceipt, len(datasets))
	if len(datasets) == 0 {
		return result, nil
	}
	query := tx.Model(&receiptRecord{})
	for i, item := range datasets {
		condition := &receiptRecord{Exchange: item.Exchange, Code: item.Code, Period: item.Period}
		if i == 0 {
			query = query.Where(condition)
		} else {
			query = query.Or(condition)
		}
	}
	records := []datasetReceipt{}
	err := query.Select("exchange,code,period,warning,imported_at,run_id").
		Order("imported_at DESC,run_id DESC").Find(&records).Error
	for _, record := range records {
		key := datasetKey{Exchange: record.Exchange, Code: record.Code, Period: record.Period}
		if _, exists := result[key]; !exists {
			result[key] = record
		}
	}
	return result, err
}
func (d *DuckDB) Bars(ctx context.Context, q domain.BarQuery) ([]domain.WarehouseBar, error) {
	q, err := q.Normalize()
	if err != nil {
		return nil, err
	}
	return transact(ctx, d, func(tx *gorm.DB) ([]domain.WarehouseBar, error) {
		result := []domain.WarehouseBar{}
		// 仅取最新的有限窗口再反转，显式 CAST 保证 ORM 扫描及 JSON 序列化都不经过 float64。
		columns := "ts::VARCHAR AS time,open::VARCHAR AS open,high::VARCHAR AS high," +
			"low::VARCHAR AS low,close::VARCHAR AS close,volume_shares,amount_cny::VARCHAR AS amount_cny,source"
		// 使用排他时间游标向历史分页，避免深 OFFSET 扫描及分页边界重复。
		query := tx.Model(&barRecord{}).Select(columns).Where(&barRecord{Exchange: q.Exchange, Code: q.Code, Period: q.Period}).
			Where("ts>=?::TIMESTAMP AND ts<?::TIMESTAMP", q.From, q.Until)
		if q.Before != "" {
			query = query.Where("ts<?::TIMESTAMP", q.Before)
		}
		err := query.Order("ts DESC").Limit(q.Limit).Scan(&result).Error
		slices.Reverse(result)
		return result, err
	})
}
func (d *DuckDB) Gaps(ctx context.Context) ([]domain.GapCandidate, error) {
	return transact(ctx, d, func(tx *gorm.DB) ([]domain.GapCandidate, error) {
		result := []domain.GapCandidate{}
		// 未知上市日期不推断缺口；缺行情还可能是停牌，不能把候选项当作确认丢失。
		stored := tx.Table("bars b").Select("1").Where("b.exchange=i.exchange AND b.code=i.code AND b.period=? AND b.ts::DATE=c.date", "day")
		err := tx.Table("instruments i").Select("i.exchange,i.code,i.name,c.date::VARCHAR AS date,'candidate_missing_or_suspended' AS reason").
			Joins("JOIN trading_calendar c ON i.exchange=c.exchange AND c.date>=i.listing_date AND c.date<=coalesce(i.delisting_date,current_date)").
			Where("c.is_open").Where("NOT EXISTS (?)", stored).Order("c.date DESC,i.exchange,i.code").Limit(200).Scan(&result).Error
		return result, err
	})
}

// Export 流程：创建私有暂存目录 → 同一事务导出所有表及行数 → 写清单 → 提交 → 原子发布。
// 取消/SQL/文件错误不会发布不完整快照；表名固定在代码中，文件路径通过绑定参数传入引擎。
func (d *DuckDB) Export(ctx context.Context) (domain.ExportResult, error) {
	_, err := d.open(ctx)
	if err != nil {
		return domain.ExportResult{}, err
	}
	root := filepath.Join(filepath.Dir(d.Path), "exports")
	if err = os.MkdirAll(root, 0755); err != nil {
		return domain.ExportResult{}, err
	}
	staging, err := os.MkdirTemp(root, ".snapshot-*.tmp")
	if err != nil {
		return domain.ExportResult{}, err
	}
	published := false
	defer func() {
		if !published {
			// 仅清理本次创建且位于 exports 内的目录，不递归删除用户传入路径。
			relative, e := filepath.Rel(root, staging)
			if e == nil && relative != "." && !strings.HasPrefix(relative, "..") && !filepath.IsAbs(relative) {
				_ = os.RemoveAll(staging)
			}
		}
	}()
	tables, err := transact(ctx, d, func(tx *gorm.DB) (map[string]int64, error) {
		counts := map[string]int64{}
		for _, table := range []string{"bars", "instruments", "trading_calendar", "adjustment_events", "provider_factors", "ingestion_receipts", "bar_revisions", "schema_migrations"} {
			if err := tx.WithContext(ctx).Exec("COPY (SELECT * FROM "+table+") TO ? (FORMAT PARQUET, COMPRESSION ZSTD)", filepath.Join(staging, table+".parquet")).Error; err != nil {
				return nil, err
			}
			var n int64
			if err := tx.WithContext(ctx).Table(table).Count(&n).Error; err != nil {
				return nil, err
			}
			counts[table] = n
		}
		manifest := map[string]any{"schemaVersion": schemaVersion, "createdAt": time.Now().UTC().Format(time.RFC3339Nano), "timezone": "Asia/Shanghai", "volumeUnit": "shares", "priceUnit": "CNY", "adjustment": "none", "completeness": "unverified", "tables": counts}
		raw, err := json.MarshalIndent(manifest, "", "  ")
		if err != nil {
			return nil, err
		}
		return counts, os.WriteFile(filepath.Join(staging, "manifest.json"), raw, 0644)
	})
	if err != nil {
		return domain.ExportResult{}, err
	}
	if err = ctx.Err(); err != nil {
		return domain.ExportResult{}, err
	}
	target := filepath.Join(root, fmt.Sprintf("snapshot-%s-%s", time.Now().UTC().Format("20060102T150405.000000000"), strings.TrimSuffix(strings.TrimPrefix(filepath.Base(staging), ".snapshot-"), ".tmp")))
	if err = os.Rename(staging, target); err != nil {
		return domain.ExportResult{}, err
	}
	published = true
	result := domain.ExportResult{Path: filepath.Join(target, "bars.parquet"), Directory: target, Tables: tables}
	entries, err := os.ReadDir(target)
	if err != nil {
		return result, err
	}
	for _, entry := range entries {
		info, err := entry.Info()
		if err != nil {
			return result, err
		}
		result.Bytes += info.Size()
	}
	return result, nil
}
