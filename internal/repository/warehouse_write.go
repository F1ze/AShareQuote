// 本文件集中实现行情入库、参考资料导入及证券/日历/因子写入；公开写操作统一使用整批事务。
package repository

import (
	"context"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"quotes/internal/domain"
)

// Ingest 的流程：核验归档 → 精确解析临时表 → 整批质量检查 → 保存旧值并合并行情
// → 更新证券资料/回执/独立因子 → 再次核验归档 → 提交事务。
// 中途任何失败均回滚数据库；已有 CSV 不删除，可供下一次幂等重放。
func (d *DuckDB) Ingest(ctx context.Context, b domain.Batch, r domain.Receipt) error {
	exchange, err := warehouseIdentity(b.Security)
	if err != nil {
		return err
	}
	if b.Category < 0 || b.Category > 12 {
		return fmt.Errorf("行情周期无效")
	}
	period := domain.CategoryName(b.Category)
	if r.VolumeUnit != "lots_100_shares" {
		return fmt.Errorf("归档成交量单位不明确，拒绝入库")
	}
	path, err := filepath.Abs(r.Path)
	if err != nil {
		return err
	}
	digest, err := fileHash(path)
	if err != nil {
		return err
	}
	if digest != r.SHA256 {
		return fmt.Errorf("归档文件在入库前发生改变，请重试")
	}
	_, err = transact(ctx, d, func(tx *gorm.DB) (struct{}, error) {
		err := ingestTx(ctx, tx, b, r, path, exchange, period)
		if err == nil {
			var hash string
			hash, err = fileHash(path)
			if err == nil && hash != digest {
				err = fmt.Errorf("归档文件在入库期间发生改变，请重试")
			}
		}
		return struct{}{}, err
	})
	return err
}
func ingestTx(ctx context.Context, tx *gorm.DB, b domain.Batch, r domain.Receipt, path, exchange, period string) error {
	// 1. CSV 全部按文本读取，价格直接转 DECIMAL；先转 DOUBLE 会丢失大成交额和小数末位。
	err := tx.WithContext(ctx).Exec(`CREATE OR REPLACE TEMP TABLE incoming AS SELECT ?::VARCHAR exchange,
 ?::VARCHAR code,?::VARCHAR period,date::TIMESTAMP ts,open::DECIMAL(18,6) open,
 high::DECIMAL(18,6) high,low::DECIMAL(18,6) low,close::DECIMAL(18,6) "close",
 round(volume::DECIMAL(24,8)*100)::BIGINT volume_shares,amount::DECIMAL(24,4) amount_cny,?::VARCHAR AS source
 FROM read_csv(?,header=true,columns={'date':'VARCHAR','open':'VARCHAR','high':'VARCHAR',
 'low':'VARCHAR','close':'VARCHAR','volume':'VARCHAR','amount':'VARCHAR'})`, exchange, b.Security.Code, period, b.Source, path).Error
	if err != nil {
		return fmt.Errorf("解析归档行情：%w", err)
	}
	// 2. 校验整批再写正式表，回执条数/边界也必须与归档内容一致，避免保存错误断点。
	var total, bad int64
	var first, last string
	err = tx.WithContext(ctx).Raw(`SELECT count(*),count(*) FILTER(WHERE ts IS NULL OR open IS NULL OR high IS NULL OR low IS NULL
 OR close IS NULL OR volume_shares IS NULL OR amount_cny IS NULL OR open<=0 OR close<=0 OR low<=0
 OR high<greatest(open,close) OR low>least(open,close) OR volume_shares<0 OR amount_cny<0),
 coalesce(strftime(min(ts),'%Y-%m-%d %H:%M'),''),coalesce(strftime(max(ts),'%Y-%m-%d %H:%M'),'') FROM incoming`).Row().Scan(&total, &bad, &first, &last)
	if err != nil {
		return err
	}
	if total == 0 || bad > 0 {
		return fmt.Errorf("行情未通过入库质量检查：%d 条，其中 %d 条无效", total, bad)
	}
	if total != int64(r.Rows) {
		return fmt.Errorf("归档条数 %d 与回执 %d 不一致", total, r.Rows)
	}
	normalizeTime := func(s string) string {
		if len(s) == 10 {
			return s + " 00:00"
		}
		return s
	}
	if first != normalizeTime(r.From) || last != normalizeTime(r.To) {
		return fmt.Errorf("归档时间边界与回执不一致")
	}
	// 3. 同主键数值发生变化才保留旧版本；重复抓取或仅切换来源不会产生虚假修订。
	err = tx.WithContext(ctx).Exec(`INSERT INTO bar_revisions SELECT old.exchange,old.code,old.period,old.ts,
 old.open,old.high,old.low,old.close,old.volume_shares,old.amount_cny,old.source,now(),?
 FROM bars old JOIN incoming new USING(exchange,code,period,ts)
 WHERE (old.open,old.high,old.low,old.close,old.volume_shares,old.amount_cny)
 IS DISTINCT FROM (new.open,new.high,new.low,new.close,new.volume_shares,new.amount_cny)`, b.RunID).Error
	if err != nil {
		return err
	}
	err = tx.WithContext(ctx).Exec(`INSERT INTO bars BY NAME SELECT *,now() ingested_at FROM incoming
 ON CONFLICT(exchange,code,period,ts) DO UPDATE SET open=excluded.open,high=excluded.high,
 low=excluded.low,close=excluded.close,volume_shares=excluded.volume_shares,amount_cny=excluded.amount_cny,
 source=excluded.source,ingested_at=excluded.ingested_at
 WHERE (bars.open,bars.high,bars.low,bars.close,bars.volume_shares,bars.amount_cny)
 IS DISTINCT FROM (excluded.open,excluded.high,excluded.low,excluded.close,excluded.volume_shares,excluded.amount_cny)`).Error
	if err != nil {
		return err
	}
	// 4. 行情、回执、元数据和因子属于同一事务；因子失败时不能宣称该证券已成功入库。
	if err = observeTx(ctx, tx, []domain.Security{b.Security}, b.Source != "archive_resume"); err != nil {
		return err
	}
	receipt := receiptRecord{Exchange: exchange, Code: b.Security.Code, Period: period, SHA256: r.SHA256, Source: b.Source,
		RequestedFrom: b.RequestedFrom, RequestedTo: b.RequestedTo, ActualFrom: r.From, ActualTo: r.To, Rows: int64(r.Rows), Warning: b.Warning, RunID: b.RunID, ImportedAt: time.Now().UTC()}
	err = tx.WithContext(ctx).Clauses(clause.OnConflict{DoNothing: true}).Create(&receipt).Error
	if err != nil {
		return err
	}
	if err = ingestAttachments(ctx, tx, b.Security, path, exchange); err != nil {
		return err
	}
	return nil
}
func ingestAttachments(ctx context.Context, tx *gorm.DB, s domain.Security, path, exchange string) error {
	prefix := filepath.Join(filepath.Dir(path), strings.ToLower(exchange)+s.Code)
	events, err := ReadAdjustmentCSV(prefix + "_gbbq.csv")
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	for _, event := range events {
		date, err := time.Parse("20060102", fmt.Sprint(event.Date))
		if err != nil {
			return fmt.Errorf("除权事件日期无效：%w", err)
		}
		for _, n := range []float64{event.Dividend, event.RightsPrice, event.Bonus, event.Rights} {
			if math.IsNaN(n) || math.IsInf(n, 0) {
				return fmt.Errorf("除权事件包含非有限数值")
			}
		}
		record := adjustmentRecord{Exchange: exchange, Code: s.Code, EventDate: date.Format("2006-01-02"), EventType: int(event.Type), Dividend: event.Dividend, RightsPrice: event.RightsPrice, Bonus: event.Bonus, Rights: event.Rights, Source: "gbbq"}
		err = tx.WithContext(ctx).Clauses(clause.OnConflict{Columns: conflictColumns("exchange", "code", "event_date", "event_type"), DoUpdates: clause.AssignmentColumns([]string{"dividend", "rights_price", "bonus", "rights", "source"})}).Create(&record).Error
		if err != nil {
			return err
		}
	}
	raw, err := os.ReadFile(prefix + "_baostock_factors.json")
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	var factors []domain.PriceFactor
	if err = json.Unmarshal(raw, &factors); err != nil {
		return err
	}
	return storeFactorsTx(ctx, tx, s, factors)
}

func warehouseIdentity(s domain.Security) (string, error) {
	exchanges := []string{"SZ", "SH", "BJ"}
	if int(s.Market) >= len(exchanges) || len(s.Code) != 6 {
		return "", fmt.Errorf("证券市场或代码无效")
	}
	for _, c := range s.Code {
		if c < '0' || c > '9' {
			return "", fmt.Errorf("证券代码无效")
		}
	}
	return exchanges[s.Market], nil
}
func validExchange(s string) bool { return s == "SH" || s == "SZ" || s == "BJ" }
func optionalDate(s string) (*string, error) {
	if s == "" {
		return nil, nil
	}
	if _, err := time.Parse("2006-01-02", s); err != nil {
		return nil, err
	}
	return &s, nil
}
func conflictColumns(names ...string) []clause.Column {
	columns := make([]clause.Column, len(names))
	for i, name := range names {
		columns[i] = clause.Column{Name: name}
	}
	return columns
}

// observeTx 的流程：把领域观察映射成持久化模型 → 按复合主键批量 upsert。
// 普通观察只补充非空参考资料；断点重放仅新增，不能覆盖后来人工校准的资料。
func observeTx(ctx context.Context, tx *gorm.DB, securities []domain.Security, allowUpdates bool) error {
	records := make([]instrumentRecord, 0, len(securities))
	now := time.Now().UTC()
	for _, s := range securities {
		exchange, err := warehouseIdentity(s)
		if err != nil {
			return err
		}
		listing, err := optionalDate(s.ListingDate)
		if err != nil {
			return fmt.Errorf("上市日期无效：%w", err)
		}
		delisting, err := optionalDate(s.DelistingDate)
		if err != nil {
			return fmt.Errorf("退市日期无效：%w", err)
		}
		if s.ListingDate != "" && s.DelistingDate != "" && s.ListingDate > s.DelistingDate {
			return fmt.Errorf("上市日期晚于退市日期")
		}
		status := s.Status
		if status == "" {
			status = "unknown"
		}
		if status != "listed" && status != "delisted" && status != "unknown" {
			return fmt.Errorf("上市状态无效")
		}
		records = append(records, instrumentRecord{Exchange: exchange, Code: s.Code, Name: s.Name, ListingDate: listing, DelistingDate: delisting, Status: status, ReferenceSource: s.ReferenceSource, FirstObservedAt: now, LastObservedAt: now})
	}
	if len(records) == 0 {
		return nil
	}
	conflict := clause.OnConflict{Columns: conflictColumns("exchange", "code"), DoNothing: !allowUpdates}
	if allowUpdates {
		conflict.DoUpdates = clause.Assignments(map[string]any{
			"name":             gorm.Expr("CASE WHEN excluded.name<>'' THEN excluded.name ELSE instruments.name END"),
			"listing_date":     gorm.Expr("coalesce(excluded.listing_date,instruments.listing_date)"),
			"delisting_date":   gorm.Expr("coalesce(excluded.delisting_date,instruments.delisting_date)"),
			"status":           gorm.Expr("CASE WHEN excluded.status<>'unknown' THEN excluded.status ELSE instruments.status END"),
			"reference_source": gorm.Expr("CASE WHEN excluded.reference_source<>'' THEN excluded.reference_source ELSE instruments.reference_source END"),
			"last_observed_at": now,
		})
	}
	return tx.WithContext(ctx).Clauses(conflict).CreateInBatches(&records, 500).Error
}
func (d *DuckDB) Observe(ctx context.Context, securities []domain.Security) error {
	_, err := transact(ctx, d, func(tx *gorm.DB) (struct{}, error) { return struct{}{}, observeTx(ctx, tx, securities, true) })
	return err
}

// storeFactorsTx 在 Go 验证日期/有限正数，再用实际 DECIMAL 类型检查可表示性，最后批量 upsert。
// 价格因子的 ORM 字段始终为文本，ParseFloat 只负责合法性检查，不参与落库转换。
func storeFactorsTx(ctx context.Context, tx *gorm.DB, security domain.Security, factors []domain.PriceFactor) error {
	exchange, err := warehouseIdentity(security)
	if err != nil {
		return err
	}
	records := make([]providerFactorRecord, 0, len(factors))
	now := time.Now().UTC()
	for _, f := range factors {
		if _, err = time.Parse("2006-01-02", f.Date); err != nil {
			return fmt.Errorf("复权日期无效：%w", err)
		}
		for _, text := range []string{f.Forward, f.Backward, f.Event} {
			n, e := strconv.ParseFloat(text, 64)
			if e != nil || n <= 0 || math.IsInf(n, 0) || math.IsNaN(n) {
				return fmt.Errorf("复权因子必须为正的有限值")
			}
		}
		records = append(records, providerFactorRecord{Exchange: exchange, Code: security.Code, EventDate: f.Date, ForwardFactor: f.Forward, BackwardFactor: f.Backward, EventFactor: f.Event, Source: "baostock", FetchedAt: now})
	}
	if len(records) == 0 {
		return nil
	}
	// 正数可能在 DECIMAL(30,15) 下舍入为零，必须用引擎的实际转换规则检查。
	raw, err := json.Marshal(factors)
	if err != nil {
		return err
	}
	var roundedToZero int64
	err = tx.WithContext(ctx).Raw(`SELECT count(*) FROM json_each(?) WHERE
 (value->>'forward')::DECIMAL(30,15)<=0 OR (value->>'backward')::DECIMAL(30,15)<=0 OR (value->>'event')::DECIMAL(30,15)<=0`, string(raw)).Row().Scan(&roundedToZero)
	if err != nil {
		return err
	}
	if roundedToZero > 0 {
		return fmt.Errorf("复权因子小于仓库支持的最小正精度")
	}
	return tx.WithContext(ctx).Clauses(clause.OnConflict{Columns: conflictColumns("exchange", "code", "event_date", "source"), DoUpdates: clause.AssignmentColumns([]string{"forward_factor", "backward_factor", "event_factor", "fetched_at"})}).CreateInBatches(&records, 500).Error
}
func (d *DuckDB) StoreFactors(ctx context.Context, s domain.Security, factors []domain.PriceFactor) error {
	_, err := transact(ctx, d, func(tx *gorm.DB) (struct{}, error) { return struct{}{}, storeFactorsTx(ctx, tx, s, factors) })
	return err
}
func (d *DuckDB) ObserveCalendar(ctx context.Context, days []domain.TradingDay) error {
	_, err := transact(ctx, d, func(tx *gorm.DB) (struct{}, error) {
		records := make([]calendarRecord, 0, len(days))
		for _, day := range days {
			if !validExchange(day.Exchange) {
				return struct{}{}, fmt.Errorf("日历市场无效")
			}
			if _, e := time.Parse("2006-01-02", day.Date); e != nil {
				return struct{}{}, e
			}
			records = append(records, calendarRecord{Exchange: day.Exchange, Date: day.Date, IsOpen: day.Open, ReferenceSource: day.Source})
		}
		return struct{}{}, storeCalendarTx(ctx, tx, records)
	})
	return err
}
func storeCalendarTx(ctx context.Context, tx *gorm.DB, records []calendarRecord) error {
	if len(records) == 0 {
		return nil
	}
	// AssignmentColumns 也会写入 false，避免 ORM 结构体 Updates 默认跳过零值导致休市标记无法修正。
	return tx.WithContext(ctx).Clauses(clause.OnConflict{Columns: conflictColumns("exchange", "date"), DoUpdates: clause.AssignmentColumns([]string{"is_open", "reference_source"})}).CreateInBatches(&records, 500).Error
}

// ImportReference 逐行解析参考 CSV，但整个文件只用一个事务：后续行无效时，先前行也全部回滚。
// 人工校准与被动 Observe 不同，允许显式清空上市/退市日期；来源始终随数据保存。
func (d *DuckDB) ImportReference(ctx context.Context, r domain.ReferenceImport) (domain.ImportResult, error) {
	if err := r.Validate(); err != nil {
		return domain.ImportResult{}, err
	}
	file, err := os.Open(r.Path)
	if err != nil {
		return domain.ImportResult{}, err
	}
	defer file.Close()
	reader := csv.NewReader(file)
	header, err := reader.Read()
	if err != nil {
		return domain.ImportResult{}, err
	}
	header[0] = strings.TrimPrefix(header[0], "\ufeff")
	columns := map[string]int{}
	for i, key := range header {
		if _, ok := columns[key]; ok {
			return domain.ImportResult{}, fmt.Errorf("CSV 重复字段：%s", key)
		}
		columns[key] = i
	}
	required := []string{"exchange", "date", "is_open"}
	if r.Kind == "instruments" {
		required = []string{"exchange", "code", "name", "status"}
	}
	for _, key := range required {
		if _, ok := columns[key]; !ok {
			return domain.ImportResult{}, fmt.Errorf("CSV 缺少字段：%s", key)
		}
	}
	return transact(ctx, d, func(tx *gorm.DB) (domain.ImportResult, error) {
		result := domain.ImportResult{}
		for line := 2; ; line++ {
			if err := ctx.Err(); err != nil {
				return result, err
			}
			record, err := reader.Read()
			if err == io.EOF {
				break
			}
			if err != nil {
				return result, fmt.Errorf("CSV 第 %d 行：%w", line, err)
			}
			value := func(key string) string {
				if i, ok := columns[key]; ok {
					return record[i]
				}
				return ""
			}
			if err = importReferenceRow(ctx, tx, r.Kind, strings.TrimSpace(r.Source), value); err != nil {
				return result, fmt.Errorf("CSV 第 %d 行：%w", line, err)
			}
			result.Rows++
		}
		return result, nil
	})
}
func importReferenceRow(ctx context.Context, tx *gorm.DB, kind, source string, value func(string) string) error {
	exchange := strings.ToUpper(value("exchange"))
	if !validExchange(exchange) {
		return fmt.Errorf("exchange 必须是 SH/SZ/BJ")
	}
	if kind == "calendar" {
		date, err := optionalDate(value("date"))
		if err != nil || date == nil {
			return fmt.Errorf("日历日期无效")
		}
		open := value("is_open")
		if open != "0" && open != "1" {
			return fmt.Errorf("is_open 必须是 0 或 1")
		}
		return storeCalendarTx(ctx, tx, []calendarRecord{{Exchange: exchange, Date: *date, IsOpen: open == "1", ReferenceSource: source}})
	}
	code, status := value("code"), value("status")
	s := domain.Security{Code: code}
	if _, err := warehouseIdentity(s); err != nil {
		return err
	}
	if status != "listed" && status != "delisted" && status != "unknown" {
		return fmt.Errorf("上市状态无效")
	}
	listing, err := optionalDate(value("listing_date"))
	if err != nil {
		return err
	}
	delisting, err := optionalDate(value("delisting_date"))
	if err != nil {
		return err
	}
	if listing != nil && delisting != nil && value("listing_date") > value("delisting_date") {
		return fmt.Errorf("上市日期晚于退市日期")
	}
	// 显式更新列包含 nil/空串，人工校准可以清空错误日期；首次观察时间不随 upsert 改写。
	now := time.Now().UTC()
	record := instrumentRecord{Exchange: exchange, Code: code, Name: value("name"), ListingDate: listing, DelistingDate: delisting, Status: status, ReferenceSource: source, FirstObservedAt: now, LastObservedAt: now}
	return tx.WithContext(ctx).Clauses(clause.OnConflict{
		Columns:   conflictColumns("exchange", "code"),
		DoUpdates: clause.AssignmentColumns([]string{"name", "listing_date", "delisting_date", "status", "reference_source", "last_observed_at"}),
	}).Create(&record).Error
}
