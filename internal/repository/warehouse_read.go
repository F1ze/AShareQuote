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
	Rows                          int64   // 全库行情条数。
	Symbols                       int64   // 至少有行情的证券数。
	Earliest                      *string // 实际最早市场时间，空库为 nil。
	Latest                        *string // 实际最晚市场时间，空库为 nil。
	Revisions                     int64   // 历史修订条数。
	FactorEvents                  int64   // 两种独立因子表的事件合计。
	CalendarDays                  int64   // 已存交易日历记录数。
	InstrumentsWithoutListingDate int64   // 尚无上市日期参考资料的证券数。
}

// Summary 在同一 ORM 事务内聚合统计、周期和近期数据集，避免各部分来自不同时间的快照。
func (d *DuckDB) Summary(ctx context.Context) (domain.WarehouseSummary, error) {
	return transact(ctx, d, func(tx *gorm.DB) (domain.WarehouseSummary, error) {
		s := domain.WarehouseSummary{Engine: "DuckDB", Ready: true, Path: d.Path, SchemaVersion: schemaVersion, VolumeUnit: "shares", PriceUnit: "CNY", Adjustment: "none", Timezone: "Asia/Shanghai", Completeness: "unverified", Periods: []domain.PeriodSummary{}, Datasets: []domain.WarehouseDataset{}}
		var totals warehouseTotals
		err := tx.Table("bars").Select(`count(*) AS rows,count(DISTINCT(exchange,code)) AS symbols,min(ts)::VARCHAR AS earliest,max(ts)::VARCHAR AS latest,
  (SELECT count(*) FROM bar_revisions) AS revisions,
  (SELECT count(*) FROM adjustment_events)+(SELECT count(*) FROM provider_factors) AS factor_events,
  (SELECT count(*) FROM trading_calendar) AS calendar_days,
  (SELECT count(*) FROM instruments WHERE listing_date IS NULL) AS instruments_without_listing_date`).Scan(&totals).Error
		if err != nil {
			return s, err
		}
		s.Rows = totals.Rows
		s.Symbols = totals.Symbols
		s.Earliest = totals.Earliest
		s.Latest = totals.Latest
		s.Revisions = totals.Revisions
		s.FactorEvents = totals.FactorEvents
		s.CalendarDays = totals.CalendarDays
		s.InstrumentsWithoutListingDate = totals.InstrumentsWithoutListingDate
		err = tx.Table("bars").Select("period,count(*) AS rows,count(DISTINCT(exchange,code)) AS symbols").Group("period").Order("period").Scan(&s.Periods).Error
		if err != nil {
			return s, err
		}
		latestReceipt := tx.Table("ingestion_receipts").Select("*,row_number() OVER (PARTITION BY exchange,code,period ORDER BY imported_at DESC,run_id DESC) AS n")
		err = tx.Table("bars b").Select(`b.exchange,b.code,i.name,b.period,count(*) AS rows,min(b.ts)::VARCHAR AS earliest,max(b.ts)::VARCHAR AS latest,
  max(b.ingested_at)::VARCHAR AS updated_at,coalesce(r.warning,'') AS warning,coalesce(i.status,'unknown') AS status,
  i.listing_date::VARCHAR AS listing_date,i.delisting_date::VARCHAR AS delisting_date`).
			Joins("LEFT JOIN instruments i ON b.exchange=i.exchange AND b.code=i.code").
			Joins("LEFT JOIN (?) r ON b.exchange=r.exchange AND b.code=r.code AND b.period=r.period AND r.n=1", latestReceipt).
			Group("b.exchange,b.code,i.name,b.period,r.warning,i.status,i.listing_date,i.delisting_date").
			Order("max(b.ingested_at) DESC,b.exchange,b.code,b.period").Limit(200).Scan(&s.Datasets).Error
		return s, err
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
		err := datasetSelection(tx, q).Select(`b.exchange,b.code,coalesce(i.name,'') AS name,b.period,count(*) AS rows,
  min(b.ts)::VARCHAR AS earliest,max(b.ts)::VARCHAR AS latest,max(b.ingested_at)::VARCHAR AS updated_at`).
			Group("b.exchange,b.code,i.name,b.period").Order("b.exchange,b.code,b.period").Limit(page.PageSize).Offset((q.Page - 1) * page.PageSize).Scan(&page.Items).Error
		return page, err
	})
}
func (d *DuckDB) Bars(ctx context.Context, q domain.BarQuery) ([]domain.WarehouseBar, error) {
	q, err := q.Normalize()
	if err != nil {
		return nil, err
	}
	return transact(ctx, d, func(tx *gorm.DB) ([]domain.WarehouseBar, error) {
		result := []domain.WarehouseBar{}
		// 仅取最新的有限窗口再反转，显式 CAST 保证 ORM 扫描及 JSON 序列化都不经过 float64。
		err := tx.Table("bars").Select(`ts::VARCHAR AS time,open::VARCHAR AS open,high::VARCHAR AS high,
  low::VARCHAR AS low,close::VARCHAR AS close,volume_shares,amount_cny::VARCHAR AS amount_cny,source`).
			Where("exchange=? AND code=? AND period=?", q.Exchange, q.Code, q.Period).
			Where("ts>=?::TIMESTAMP AND ts<?::TIMESTAMP", q.From, q.Until).Order("ts DESC").Limit(q.Limit).Scan(&result).Error
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
