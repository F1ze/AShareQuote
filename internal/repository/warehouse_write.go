// 本文件集中实现行情入库、参考资料导入及证券/日历/因子写入；公开写操作统一使用整批事务。
package repository

import (
	"context"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"math/big"
	"os"
	"path/filepath"
	"slices"
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
	// 1. Go 逐行解析标准归档，所有数值按十进制精确舍入，不经过 float64 或 SQL 临时表。
	incoming, err := readArchiveBars(ctx, path, exchange, b.Security.Code, period, b.Source)
	if err != nil {
		return fmt.Errorf("解析归档行情：%w", err)
	}

	// 2. 在 Go 中校验整批，回执条数/边界必须与实际归档一致，避免保存错误断点。
	first, last, bad := archiveQuality(incoming)
	if len(incoming) == 0 || bad > 0 {
		return fmt.Errorf("行情未通过入库质量检查：%d 条，其中 %d 条无效", len(incoming), bad)
	}
	if len(incoming) != r.Rows {
		return fmt.Errorf("归档条数 %d 与回执 %d 不一致", len(incoming), r.Rows)
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

	// 3. 通过 GORM 读取重叠主键、保存实际变化的旧值，再批量 upsert 新值。
	if err = mergeBarsTx(ctx, tx, incoming, exchange, b.Security.Code, period, first, last, b.RunID); err != nil {
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

// readArchiveBars 只接受标准归档列；日期、价格和成交量都在进入 ORM 前完成确定性转换。
func readArchiveBars(ctx context.Context, path, exchange, code, period, source string) ([]barRecord, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	reader := csv.NewReader(file)
	header, err := reader.Read()
	if err != nil {
		return nil, err
	}
	if len(header) > 0 {
		header[0] = strings.TrimPrefix(header[0], "\ufeff")
	}
	want := []string{"date", "open", "high", "low", "close", "volume", "amount"}
	if len(header) != len(want) {
		return nil, fmt.Errorf("行情 CSV 需要 date,open,high,low,close,volume,amount 列")
	}
	for i := range want {
		if header[i] != want[i] {
			return nil, fmt.Errorf("CSV 第 %d 列应为 %s", i+1, want[i])
		}
	}

	records := []barRecord{}
	for line := 2; ; line++ {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		row, err := reader.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("CSV 第 %d 行：%w", line, err)
		}
		record, err := parseArchiveBar(row, exchange, code, period, source)
		if err != nil {
			return nil, fmt.Errorf("CSV 第 %d 行：%w", line, err)
		}
		records = append(records, record)
	}
	return records, nil
}

func parseArchiveBar(row []string, exchange, code, period, source string) (barRecord, error) {
	result := barRecord{Exchange: exchange, Code: code, Period: period, Source: source}
	layout := "2006-01-02"
	if len(row[0]) > 10 {
		layout = "2006-01-02 15:04"
	}
	date, err := time.ParseInLocation(layout, row[0], domain.ExchangeTime)
	if err != nil {
		return result, fmt.Errorf("日期无效：%w", err)
	}
	result.Time = date.Format("2006-01-02 15:04")

	prices := []*string{&result.Open, &result.High, &result.Low, &result.Close}
	for i, target := range prices {
		value, err := normalizeDecimal(row[i+1], 18, 6)
		if err != nil {
			return result, fmt.Errorf("%s：%w", wantArchiveColumn(i+1), err)
		}
		*target = value
	}
	lotsText, err := normalizeDecimal(row[5], 24, 8)
	if err != nil {
		return result, fmt.Errorf("volume：%w", err)
	}
	lots, _ := new(big.Rat).SetString(lotsText)
	shares := new(big.Rat).Mul(lots, big.NewRat(100, 1))
	sharesText, err := normalizeDecimal(shares.RatString(), 19, 0)
	if err != nil {
		return result, fmt.Errorf("volume 换算为股：%w", err)
	}
	result.VolumeShares, err = strconv.ParseInt(sharesText, 10, 64)
	if err != nil {
		return result, fmt.Errorf("volume 超出 BIGINT 范围：%w", err)
	}
	result.AmountCNY, err = normalizeDecimal(row[6], 24, 4)
	if err != nil {
		return result, fmt.Errorf("amount：%w", err)
	}
	return result, nil
}

func wantArchiveColumn(index int) string {
	return []string{"date", "open", "high", "low", "close", "volume", "amount"}[index]
}

// normalizeDecimal 模拟 DuckDB DECIMAL 的定标、四舍五入和精度边界，正负数均采用远离零的半数舍入。
func normalizeDecimal(text string, precision, scale int) (string, error) {
	exact, ok := new(big.Rat).SetString(text)
	if !ok {
		return "", fmt.Errorf("不是有效十进制数")
	}
	factor := new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(scale)), nil)
	numerator := new(big.Int).Mul(exact.Num(), factor)
	rounded, remainder := new(big.Int), new(big.Int)
	rounded.QuoRem(numerator, exact.Denom(), remainder)
	if new(big.Int).Lsh(new(big.Int).Abs(remainder), 1).Cmp(exact.Denom()) >= 0 {
		if numerator.Sign() < 0 {
			rounded.Sub(rounded, big.NewInt(1))
		} else {
			rounded.Add(rounded, big.NewInt(1))
		}
	}
	limit := new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(precision)), nil)
	if new(big.Int).Abs(rounded).Cmp(limit) >= 0 {
		return "", fmt.Errorf("超出 DECIMAL(%d,%d) 范围", precision, scale)
	}
	return formatScaledDecimal(rounded, scale), nil
}

func formatScaledDecimal(value *big.Int, scale int) string {
	negative := value.Sign() < 0
	digits := new(big.Int).Abs(value).String()
	if scale > 0 {
		if len(digits) <= scale {
			digits = strings.Repeat("0", scale-len(digits)+1) + digits
		}
		split := len(digits) - scale
		digits = digits[:split] + "." + digits[split:]
	}
	if negative {
		digits = "-" + digits
	}
	return digits
}

// archiveQuality 使用十进制有理数检查 OHLCV，不经过 float64，也不从文件行序推断时间边界。
func archiveQuality(records []barRecord) (first, last string, bad int64) {
	for _, record := range records {
		values := make([]*big.Rat, 5)
		valid := record.Time != "" && record.VolumeShares >= 0
		for i, text := range []string{record.Open, record.High, record.Low, record.Close, record.AmountCNY} {
			value, ok := new(big.Rat).SetString(text)
			if !ok {
				valid = false
				continue
			}
			values[i] = value
		}
		if valid {
			open, high, low, close, amount := values[0], values[1], values[2], values[3], values[4]
			valid = open.Sign() > 0 && close.Sign() > 0 && low.Sign() > 0 && amount.Sign() >= 0 &&
				high.Cmp(open) >= 0 && high.Cmp(close) >= 0 && low.Cmp(open) <= 0 && low.Cmp(close) <= 0
		}
		if !valid {
			bad++
			continue
		}
		if first == "" || record.Time < first {
			first = record.Time
		}
		if last == "" || record.Time > last {
			last = record.Time
		}
	}
	return first, last, bad
}

// mergeBarsTx 只写入新增或数值变化的记录；同值重放不会更新来源和入库时间，也不会制造修订。
func mergeBarsTx(ctx context.Context, tx *gorm.DB, incoming []barRecord, exchange, code, period, first, last, runID string) error {
	stored := []barRecord{}
	columns := []string{
		"exchange", "code", "period", "strftime(ts,'%Y-%m-%d %H:%M') AS ts",
		"open::VARCHAR AS open", "high::VARCHAR AS high", "low::VARCHAR AS low", "close::VARCHAR AS close",
		"volume_shares", "amount_cny::VARCHAR AS amount_cny", "source",
	}
	err := tx.WithContext(ctx).Model(&barRecord{}).Select(columns).
		Where(&barRecord{Exchange: exchange, Code: code, Period: period}).
		Where("ts >= ?::TIMESTAMP AND ts <= ?::TIMESTAMP", first, last).Find(&stored).Error
	if err != nil {
		return err
	}
	existing := make(map[string]barRecord, len(stored))
	for _, record := range stored {
		existing[record.Time] = record
	}

	now := time.Now().UTC()
	changes := make([]barRecord, 0, len(incoming))
	revisions := []revisionRecord{}
	for _, record := range incoming {
		old, found := existing[record.Time]
		if found && sameBarValues(old, record) {
			continue
		}
		record.IngestedAt = now
		changes = append(changes, record)
		if found {
			revisions = append(revisions, revisionRecord{Exchange: old.Exchange, Code: old.Code, Period: old.Period, Time: old.Time,
				OldOpen: old.Open, OldHigh: old.High, OldLow: old.Low, OldClose: old.Close, OldVolumeShares: old.VolumeShares,
				OldAmountCNY: old.AmountCNY, OldSource: old.Source, ReplacedAt: now, RunID: runID})
		}
	}
	if len(revisions) > 0 {
		if err := tx.WithContext(ctx).CreateInBatches(&revisions, 500).Error; err != nil {
			return err
		}
	}
	if len(changes) == 0 {
		return nil
	}
	return tx.WithContext(ctx).Clauses(clause.OnConflict{
		Columns: conflictColumns("exchange", "code", "period", "ts"),
		DoUpdates: clause.AssignmentColumns([]string{
			"open", "high", "low", "close", "volume_shares", "amount_cny", "source", "ingested_at",
		}),
	}).CreateInBatches(&changes, 500).Error
}

func sameBarValues(a, b barRecord) bool {
	return a.Open == b.Open && a.High == b.High && a.Low == b.Low && a.Close == b.Close &&
		a.VolumeShares == b.VolumeShares && a.AmountCNY == b.AmountCNY
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
	if !allowUpdates {
		return tx.WithContext(ctx).Clauses(clause.OnConflict{DoNothing: true}).CreateInBatches(&records, 500).Error
	}
	// 相邻记录按可更新字段分批，保持观察顺序；未知字段根本不出现在更新列中，无需 CASE/COALESCE SQL。
	for start := 0; start < len(records); {
		columns := observationColumns(records[start])
		end := start + 1
		for end < len(records) && slices.Equal(columns, observationColumns(records[end])) {
			end++
		}
		batch := records[start:end]
		if err := tx.WithContext(ctx).Clauses(clause.OnConflict{Columns: conflictColumns("exchange", "code"), DoUpdates: clause.AssignmentColumns(columns)}).CreateInBatches(&batch, 500).Error; err != nil {
			return err
		}
		start = end
	}
	return nil
}

// observationColumns 只列出本次确实观察到的资料；首见时间永远不参与更新。
func observationColumns(record instrumentRecord) []string {
	columns := []string{"last_observed_at"}
	if record.Name != "" {
		columns = append(columns, "name")
	}
	if record.ListingDate != nil {
		columns = append(columns, "listing_date")
	}
	if record.DelistingDate != nil {
		columns = append(columns, "delisting_date")
	}
	if record.Status != "unknown" {
		columns = append(columns, "status")
	}
	if record.ReferenceSource != "" {
		columns = append(columns, "reference_source")
	}
	return columns
}

func (d *DuckDB) Observe(ctx context.Context, securities []domain.Security) error {
	_, err := transact(ctx, d, func(tx *gorm.DB) (struct{}, error) { return struct{}{}, observeTx(ctx, tx, securities, true) })
	return err
}

// storeFactorsTx 在 Go 验证日期和 DECIMAL(30,15) 可表示性，随后用 ORM 批量 upsert。
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
			if err := validateFactorDecimal(text); err != nil {
				return err
			}
		}
		records = append(records, providerFactorRecord{Exchange: exchange, Code: security.Code, EventDate: f.Date, ForwardFactor: f.Forward, BackwardFactor: f.Backward, EventFactor: f.Event, Source: "baostock", FetchedAt: now})
	}
	if len(records) == 0 {
		return nil
	}

	return tx.WithContext(ctx).Clauses(clause.OnConflict{Columns: conflictColumns("exchange", "code", "event_date", "source"), DoUpdates: clause.AssignmentColumns([]string{"forward_factor", "backward_factor", "event_factor", "fetched_at"})}).CreateInBatches(&records, 500).Error
}

// validateFactorDecimal 用有理数精确模拟 DECIMAL(30,15) 的正数四舍五入，防止浮点误差误判边界。
// 初步有限值检查也限制异常指数，避免解析恶意超大指数时分配过多内存；落库仍绑定原始文本。
func validateFactorDecimal(text string) error {
	n, err := strconv.ParseFloat(text, 64)
	if err != nil || n <= 0 || math.IsNaN(n) || math.IsInf(n, 0) {
		return fmt.Errorf("复权因子必须为正的有限值")
	}
	exact, ok := new(big.Rat).SetString(text)
	if !ok || exact.Sign() <= 0 {
		return fmt.Errorf("复权因子必须是有效十进制文本")
	}
	scaled := new(big.Int).Mul(exact.Num(), big.NewInt(1_000_000_000_000_000))
	rounded, remainder := new(big.Int), new(big.Int)
	rounded.QuoRem(scaled, exact.Denom(), remainder)
	if remainder.Lsh(remainder, 1).Cmp(exact.Denom()) >= 0 {
		rounded.Add(rounded, big.NewInt(1))
	}
	if rounded.Sign() == 0 {
		return fmt.Errorf("复权因子小于仓库支持的最小正精度")
	}
	if rounded.Cmp(new(big.Int).Exp(big.NewInt(10), big.NewInt(30), nil)) >= 0 {
		return fmt.Errorf("复权因子超出 DECIMAL(30,15) 范围")
	}
	return nil
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
