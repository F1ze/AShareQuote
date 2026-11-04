package repository

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"gorm.io/gorm"

	"quotes/internal/domain"
)

func fixtureDB(t *testing.T) *DuckDB {
	t.Helper()
	d := NewDuckDB(filepath.Join(t.TempDir(), "中文目录", "quotes.duckdb"))
	t.Cleanup(func() {
		if err := d.Close(); err != nil {
			t.Error(err)
		}
	})
	return d
}
func mustOK(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}
func fixtureArchive(t *testing.T, d *DuckDB, body, run, source string) (domain.Batch, domain.Receipt) {
	t.Helper()
	mustOK(t, d.Prepare())
	path := filepath.Join(filepath.Dir(d.Path), "sh600000_day.csv")
	if body == "" {
		body = "2024-01-02,10,12,9,11,0.95,1000\n2024-01-03,11,13,10,12,200,240000\n"
	}
	mustOK(t, os.WriteFile(path, []byte("date,open,high,low,close,volume,amount\n"+body), 0644))
	rows := strings.Split(strings.TrimSpace(body), "\n")
	hash, err := fileHash(path)
	mustOK(t, err)
	b := domain.Batch{Security: domain.Security{Market: 1, Code: "600000", Name: "浦发银行"}, Category: 9, Source: source, RequestedFrom: "2024-01-01", RequestedTo: "2024-01-03", RunID: run}
	r := domain.Receipt{Path: path, SHA256: hash, Rows: len(rows), From: strings.Split(rows[0], ",")[0], To: strings.Split(rows[len(rows)-1], ",")[0], VolumeUnit: "lots_100_shares"}
	return b, r
}
func ingestFixture(t *testing.T, d *DuckDB, body, run, source string) {
	t.Helper()
	b, r := fixtureArchive(t, d, body, run, source)
	mustOK(t, d.Ingest(context.Background(), b, r))
}
func summaryFixture(t *testing.T, d *DuckDB) domain.WarehouseSummary {
	t.Helper()
	s, err := d.Summary(context.Background())
	mustOK(t, err)
	return s
}
func referenceFixture(t *testing.T, d *DuckDB, kind, body string) error {
	t.Helper()
	path := filepath.Join(filepath.Dir(d.Path), kind+".csv")
	mustOK(t, os.WriteFile(path, []byte(body), 0644))
	_, err := d.ImportReference(context.Background(), domain.ReferenceImport{Kind: kind, Path: path, Source: "curated fixture"})
	return err
}
func scalarFixture[T any](t *testing.T, d *DuckDB, query string, args ...any) T {
	t.Helper()
	db, err := d.open(context.Background())
	mustOK(t, err)
	var value T
	mustOK(t, db.Raw(query, args...).Row().Scan(&value))
	return value
}

// 真实引擎测试禁止依赖 Python：环境变量故意指向不存在的解释器。
func TestNativeWarehouseIdempotenceUnitsRevisionsAndSearch(t *testing.T) {
	t.Setenv("QFLUX_PYTHON", filepath.Join(t.TempDir(), "no-python"))
	d := fixtureDB(t)
	ctx := context.Background()
	ingestFixture(t, d, "", "run1", "fixture")
	ingestFixture(t, d, "", "run1", "fixture")
	s := summaryFixture(t, d)
	if s.Rows != 2 || s.Symbols != 1 || s.Revisions != 0 {
		t.Fatalf("unexpected summary %+v", s)
	}
	page, err := d.Datasets(ctx, domain.DatasetQuery{Text: "浦发", Period: "day"})
	mustOK(t, err)
	if page.Total != 1 || len(page.Items) != 1 || page.Page != 1 {
		t.Fatalf("page %+v", page)
	}
	page, err = d.Datasets(ctx, domain.DatasetQuery{Text: "600000' OR 1=1"})
	mustOK(t, err)
	if page.Total != 0 {
		t.Fatal("search escaped bound parameter")
	}
	bars, err := d.Bars(ctx, domain.BarQuery{Exchange: "SH", Code: "600000", Period: "day", From: "2024-01-01", To: "2024-01-03", Limit: 100})
	mustOK(t, err)
	if len(bars) != 2 || bars[0].VolumeShares != 95 || bars[0].Close != "11.000000" || bars[0].Time > bars[1].Time {
		t.Fatalf("bars %+v", bars)
	}
	ingestFixture(t, d, "2024-01-02,10,13,9,12,100,120000\n", "correction", "fixture")
	ingestFixture(t, d, "2024-01-02,10,13,9,12,100,120000\n", "repeat", "another_source")
	s = summaryFixture(t, d)
	if s.Rows != 2 || s.Revisions != 1 {
		t.Fatalf("unexpected revisions %+v", s)
	}
	if n := scalarFixture[int64](t, d, "SELECT count(*) FROM ingestion_receipts"); n != 3 {
		t.Fatalf("receipts %d", n)
	}
}
func TestNativeWarehouseExactDecimals(t *testing.T) {
	d := fixtureDB(t)
	ctx := context.Background()
	ingestFixture(t, d, "2024-01-02,123456789.123456,123456789.123457,123456789.123455,123456789.123456,0.95,12345678901234567890.1234\n", "precise", "fixture")
	bars, err := d.Bars(ctx, domain.BarQuery{Exchange: "SH", Code: "600000", Period: "day", From: "2024-01-02", To: "2024-01-02"})
	mustOK(t, err)
	if bars[0].Open != "123456789.123456" || bars[0].AmountCNY != "12345678901234567890.1234" {
		t.Fatalf("precision lost: %+v", bars[0])
	}
	mustOK(t, d.StoreFactors(ctx, domain.Security{Market: 1, Code: "600000"}, []domain.PriceFactor{{Date: "2024-01-02", Forward: "0.950000000000001", Backward: "1.25", Event: "0.95"}}))
	if text := scalarFixture[string](t, d, "SELECT forward_factor::VARCHAR FROM provider_factors"); text != "0.950000000000001" {
		t.Fatal(text)
	}
}
func TestNativeWarehouseBadBatchRollsBackAllTables(t *testing.T) {
	d := fixtureDB(t)
	ctx := context.Background()
	ingestFixture(t, d, "", "initial", "fixture")
	cases := []struct {
		name, body string
		mutate     func(*domain.Receipt)
	}{
		{"invalid OHLC", "2024-01-02,10,12,9,11,1,100\n2024-01-04,10,8,9,11,1,100\n", nil},
		{"empty price", "2024-01-04,,12,9,11,1,100\n", nil},
		{"hash", "", func(r *domain.Receipt) { r.SHA256 = "bad" }},
		{"units", "", func(r *domain.Receipt) { r.VolumeUnit = "shares" }},
		{"count", "", func(r *domain.Receipt) { r.Rows++ }},
		{"coverage", "", func(r *domain.Receipt) { r.From = "2023-01-01" }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			b, r := fixtureArchive(t, d, tc.body, tc.name, "fixture")
			if tc.mutate != nil {
				tc.mutate(&r)
			}
			if err := d.Ingest(ctx, b, r); err == nil {
				t.Fatal("expected rejected batch")
			}
			s := summaryFixture(t, d)
			if s.Rows != 2 || s.Revisions != 0 || scalarFixture[int64](t, d, "SELECT count(*) FROM ingestion_receipts") != 1 {
				t.Fatalf("partial commit %+v", s)
			}
		})
	}
}
func TestNativeWarehouseFactorReplayAndFailureRollback(t *testing.T) {
	d := fixtureDB(t)
	mustOK(t, d.Prepare())
	path := filepath.Join(filepath.Dir(d.Path), "sh600000_baostock_factors.json")
	mustOK(t, os.WriteFile(path, []byte(`[{"date":"2024-01-02","forward":"0.950000000000001","backward":"1.25","event":"0.95"}]`), 0644))
	ingestFixture(t, d, "", "run1", "fixture")
	ingestFixture(t, d, "", "resume", "archive_resume")
	if s := summaryFixture(t, d); s.FactorEvents != 1 || s.SchemaVersion != 2 {
		t.Fatalf("%+v", s)
	}
	mustOK(t, os.WriteFile(path, []byte(`[{"date":"2024-01-02","forward":"-1","backward":"1","event":"1"}]`), 0644))
	b, r := fixtureArchive(t, d, "2024-01-02,10,13,9,12,100,120000\n", "bad_factor", "fixture")
	if err := d.Ingest(context.Background(), b, r); err == nil {
		t.Fatal("expected factor validation")
	}
	s := summaryFixture(t, d)
	if s.Revisions != 0 || s.FactorEvents != 1 || scalarFixture[int64](t, d, "SELECT count(*) FROM ingestion_receipts") != 2 {
		t.Fatalf("factor failure committed %+v", s)
	}
}
func TestNativeWarehouseReferenceRollbackGapsAndResume(t *testing.T) {
	d := fixtureDB(t)
	ctx := context.Background()
	ingestFixture(t, d, "", "run1", "fixture")
	mustOK(t, referenceFixture(t, d, "instruments", "\ufeffexchange,code,name,listing_date,delisting_date,status\nSH,600000,人工校准名称,2024-01-02,2024-01-04,delisted\n"))
	mustOK(t, referenceFixture(t, d, "calendar", "exchange,date,is_open\nSH,2024-01-02,1\nSH,2024-01-03,1\nSH,2024-01-04,1\n"))
	gaps, err := d.Gaps(ctx)
	mustOK(t, err)
	if len(gaps) != 1 || gaps[0].Date != "2024-01-04" || gaps[0].Reason != "candidate_missing_or_suspended" {
		t.Fatalf("gaps %+v", gaps)
	}
	if err = referenceFixture(t, d, "calendar", "exchange,date,is_open\nSH,2024-01-05,1\nBAD,2024-01-06,1\n"); err == nil {
		t.Fatal("expected reference failure")
	}
	if s := summaryFixture(t, d); s.CalendarDays != 3 || s.Completeness != "unverified" {
		t.Fatalf("%+v", s)
	}
	ingestFixture(t, d, "", "replay", "archive_resume")
	item := summaryFixture(t, d).Datasets[0]
	if item.Name == nil || *item.Name != "人工校准名称" || item.Status != "delisted" || item.DelistingDate == nil || *item.DelistingDate != "2024-01-04" {
		t.Fatalf("replay overwrote reference %+v", item)
	}
}
func TestNativeWarehouseParquetSnapshotAllTables(t *testing.T) {
	d := fixtureDB(t)
	mustOK(t, d.Prepare())
	mustOK(t, os.WriteFile(filepath.Join(filepath.Dir(d.Path), "sh600000_gbbq.csv"), []byte("date,type,dividend,rights_price,bonus,rights\n20240102,1,1.0,0,0,0\n"), 0644))
	ingestFixture(t, d, "", "run1", "fixture")
	if summaryFixture(t, d).FactorEvents != 1 {
		t.Fatal("missing gbbq")
	}
	result, err := d.Export(context.Background())
	mustOK(t, err)
	if result.Bytes <= 0 || result.Tables["bars"] != 2 || len(result.Tables) != 8 {
		t.Fatalf("%+v", result)
	}
	for table, count := range result.Tables {
		if n := scalarFixture[int64](t, d, "SELECT count(*) FROM read_parquet(?)", filepath.Join(result.Directory, table+".parquet")); n != count {
			t.Fatalf("snapshot table %s: %d != %d", table, n, count)
		}
	}
	raw, err := os.ReadFile(filepath.Join(result.Directory, "manifest.json"))
	mustOK(t, err)
	var manifest struct {
		Version int              `json:"schemaVersion"`
		Tables  map[string]int64 `json:"tables"`
	}
	mustOK(t, json.Unmarshal(raw, &manifest))
	if manifest.Version != 2 || manifest.Tables["bars"] != 2 {
		t.Fatalf("manifest %s", raw)
	}
	entries, err := os.ReadDir(filepath.Dir(result.Directory))
	mustOK(t, err)
	if len(entries) != 1 || strings.HasPrefix(entries[0].Name(), ".") {
		t.Fatalf("partial snapshots %+v", entries)
	}
}
func TestNativeWarehouseMigrationsAndFutureVersion(t *testing.T) {
	ctx := context.Background()
	d := fixtureDB(t)
	mustOK(t, d.Check(ctx))
	db, err := d.open(ctx)
	mustOK(t, err)
	err = db.Exec("DROP TABLE provider_factors; DELETE FROM schema_migrations WHERE version=2").Error
	mustOK(t, err)
	mustOK(t, d.Close())
	upgraded := NewDuckDB(d.Path)
	defer upgraded.Close()
	mustOK(t, upgraded.Check(ctx))
	if v := scalarFixture[int](t, upgraded, "SELECT max(version) FROM schema_migrations"); v != 2 {
		t.Fatalf("migration version %d", v)
	}
	db, err = upgraded.open(ctx)
	mustOK(t, err)
	err = db.Exec("INSERT INTO schema_migrations(version) VALUES(99)").Error
	mustOK(t, err)
	mustOK(t, upgraded.Close())
	future := NewDuckDB(d.Path)
	defer future.Close()
	if err = future.Check(ctx); err == nil || !strings.Contains(err.Error(), "99") {
		t.Fatalf("future version accepted: %v", err)
	}
}
func TestNativeWarehouseCancelRollsBackAndConnectionRemainsUsable(t *testing.T) {
	d := fixtureDB(t)
	ctx, cancel := context.WithCancel(context.Background())
	_, err := transact(ctx, d, func(tx *gorm.DB) (struct{}, error) {
		err := tx.Exec("INSERT INTO trading_calendar VALUES('SH','2024-01-02',true,'cancelled')").Error
		cancel()
		return struct{}{}, err
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("cancel: %v", err)
	}
	if n := summaryFixture(t, d).CalendarDays; n != 0 {
		t.Fatalf("cancel committed %d", n)
	}
	// 连接被占用时，新的请求也应按自己的 deadline 结束，而不是等前一个任务完成。
	db, err := d.open(context.Background())
	mustOK(t, err)
	sqlDB, err := db.DB()
	mustOK(t, err)
	conn, err := sqlDB.Conn(context.Background())
	mustOK(t, err)
	deadline, stop := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer stop()
	_, err = d.Summary(deadline)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("connection wait not cancellable: %v", err)
	}
	mustOK(t, conn.Close())
	mustOK(t, d.Check(context.Background()))
}
func TestNativeWarehouseCloseDoesNotReopen(t *testing.T) {
	d := fixtureDB(t)
	mustOK(t, d.Close())
	if err := d.Check(context.Background()); err == nil {
		t.Fatal("closed warehouse reopened")
	}
}
func TestNativeWarehouseCancelsExecutingSQL(t *testing.T) {
	d := fixtureDB(t)
	mustOK(t, d.Check(context.Background()))
	ctx, stop := context.WithTimeout(context.Background(), 40*time.Millisecond)
	defer stop()
	started := time.Now()
	_, err := transact(ctx, d, func(tx *gorm.DB) (int64, error) {
		var result int64
		err := tx.Raw("SELECT sum(a.range*b.range) FROM range(1000000000) a CROSS JOIN range(1000000000) b").Row().Scan(&result)
		return result, err
	})
	if err == nil || ctx.Err() == nil || time.Since(started) > 3*time.Second {
		t.Fatalf("executing SQL did not cancel promptly: %v", err)
	}
	mustOK(t, d.ObserveCalendar(context.Background(), []domain.TradingDay{{Exchange: "SH", Date: "2024-01-02", Open: true, Source: "after cancellation"}}))
	if n := summaryFixture(t, d).CalendarDays; n != 1 {
		t.Fatalf("connection unusable after interrupt: %d", n)
	}
}

func TestNativeWarehouseRejectsRoundedZeroFactor(t *testing.T) {
	d := fixtureDB(t)
	err := d.StoreFactors(context.Background(), domain.Security{Market: 1, Code: "600000"}, []domain.PriceFactor{{Date: "2024-01-02", Forward: "0.0000000000000001", Backward: "1", Event: "1"}})
	if err == nil {
		t.Fatal("positive factor rounded to zero was accepted")
	}
	if n := summaryFixture(t, d).FactorEvents; n != 0 {
		t.Fatalf("invalid factor committed: %d", n)
	}
}

// 超过单批上限的真实数据验证批量 Create 与外层事务配合，不得隐式创建 DuckDB 不支持的嵌套事务。
func TestNativeWarehouseORMBulkAndExplicitZeroValues(t *testing.T) {
	d := fixtureDB(t)
	ctx := context.Background()
	securities := make([]domain.Security, 620)
	days := make([]domain.TradingDay, len(securities))
	start := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	for i := range securities {
		securities[i] = domain.Security{Market: 1, Code: fmt.Sprintf("%06d", 600000+i), Name: "批量证券", ListingDate: "2024-01-01", Status: "listed", ReferenceSource: "initial"}
		days[i] = domain.TradingDay{Exchange: "SH", Date: start.AddDate(0, 0, i).Format("2006-01-02"), Open: true, Source: "initial"}
	}
	mustOK(t, d.Observe(ctx, securities))
	mustOK(t, d.ObserveCalendar(ctx, days))
	if n := scalarFixture[int64](t, d, "SELECT count(*) FROM instruments"); n != 620 {
		t.Fatalf("bulk instruments: %d", n)
	}
	for i := range days {
		days[i].Open = false
		days[i].Source = "corrected"
	}
	mustOK(t, d.ObserveCalendar(ctx, days))
	if n := scalarFixture[int64](t, d, "SELECT count(*) FROM trading_calendar WHERE NOT is_open AND reference_source='corrected'"); n != 620 {
		t.Fatalf("false updates skipped: %d", n)
	}
	first := scalarFixture[string](t, d, "SELECT first_observed_at::VARCHAR FROM instruments WHERE code='600000'")
	mustOK(t, d.Observe(ctx, []domain.Security{{Market: 1, Code: "600000", ReferenceSource: "observation without metadata"}}))
	if name := scalarFixture[string](t, d, "SELECT name FROM instruments WHERE code='600000'"); name != "批量证券" {
		t.Fatalf("empty observation erased name: %q", name)
	}
	// 人工校准允许清空错误日期和名称，这与被动观察的非空补充规则不同。
	mustOK(t, referenceFixture(t, d, "instruments", "exchange,code,name,listing_date,delisting_date,status\nSH,600000,,,,unknown\n"))
	if n := scalarFixture[int64](t, d, "SELECT count(*) FROM instruments WHERE code='600000' AND name='' AND listing_date IS NULL AND delisting_date IS NULL AND status='unknown'"); n != 1 {
		t.Fatal("ORM skipped explicit null/empty fields")
	}
	if after := scalarFixture[string](t, d, "SELECT first_observed_at::VARCHAR FROM instruments WHERE code='600000'"); after != first {
		t.Fatalf("first observation changed: %q -> %q", first, after)
	}
}

// 聚合总数与分页使用独立 Statement；第二页及空页不能受上一次 Count/Limit 污染。
func TestNativeWarehouseORMGroupedPagination(t *testing.T) {
	d := fixtureDB(t)
	ctx := context.Background()
	db, err := d.open(ctx)
	mustOK(t, err)
	mustOK(t, db.Exec(`INSERT INTO bars SELECT 'SH',lpad((600000+range)::VARCHAR,6,'0'),'day',
 '2024-01-02'::TIMESTAMP,10,12,9,11,100,1000,'fixture',now() FROM range(60)`).Error)
	first, err := d.Datasets(ctx, domain.DatasetQuery{Period: "day", Text: "600", Page: 1})
	mustOK(t, err)
	second, err := d.Datasets(ctx, domain.DatasetQuery{Period: "day", Text: "600", Page: 2})
	mustOK(t, err)
	empty, err := d.Datasets(ctx, domain.DatasetQuery{Period: "day", Text: "600", Page: 3})
	mustOK(t, err)
	if first.Total != 60 || len(first.Items) != 50 || second.Total != 60 || len(second.Items) != 10 || second.Items[0].Code != "600050" || empty.Total != 60 || len(empty.Items) != 0 {
		t.Fatalf("pagination: first=%+v second=%+v empty=%+v", first, second, empty)
	}
}
