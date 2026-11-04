package repository

import (
	"context"
	"crypto/sha256"
	"encoding/csv"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"quotes/internal/domain"
)

var ExchangeTime = domain.ExchangeTime

// CSVArchive 保存统一格式的未复权行情；同一证券和周期由任务层保证不会并发合并写入。
type CSVArchive struct {
	Directory string // 标准 CSV 及 .meta.json 所在目录，临时文件也在此目录创建以便最终替换。
}

// ReadCSV 只解析标准列及北京时间，不猜测外部文件的成交量单位。
// 外部单位由来源适配器转换，数值与 OHLC 关系由 Validate 单独检查。
func ReadCSV(path string) ([]domain.Bar, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	r := csv.NewReader(f)
	header, err := r.Read()
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
	var out []domain.Bar
	for line := 2; ; line++ {
		record, err := r.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("CSV 第 %d 行：%w", line, err)
		}
		layout := "2006-01-02"
		if len(record[0]) > 10 {
			layout = "2006-01-02 15:04"
		}
		date, err := time.ParseInLocation(layout, record[0], ExchangeTime)
		if err != nil {
			return nil, fmt.Errorf("CSV 第 %d 行日期无效", line)
		}
		values := make([]float64, 6)
		for i := range values {
			values[i], err = strconv.ParseFloat(record[i+1], 64)
			if err != nil {
				return nil, fmt.Errorf("CSV 第 %d 行数值无效", line)
			}
		}
		out = append(out, domain.Bar{Date: date, Open: values[0], High: values[1], Low: values[2], Close: values[3], Volume: values[4], Amount: values[5]})
	}
	return out, nil
}

// Commit 先校验新旧行情，再按时间合并；重叠记录以后来的批次为准，范围外旧历史继续保留。
// 临时文件与目标文件放在同一目录，写完、同步并关闭后才替换，避免暴露半个 CSV。
// 行情与元数据不是跨文件事务；元数据写入失败时返回错误，后续不得写成功断点。
func (a *CSVArchive) Commit(ctx context.Context, b domain.Batch) (domain.Receipt, error) {
	// 第 1 步：验证证券标识及新批次质量，再读取并验证旧文件；旧文件损坏时拒绝覆盖。
	if b.Security.Market > 2 || len(b.Security.Code) != 6 || strings.IndexFunc(b.Security.Code, func(r rune) bool { return r < '0' || r > '9' }) >= 0 {
		return domain.Receipt{}, fmt.Errorf("证券标识无效，拒绝写入归档")
	}
	if err := domain.ValidateBars(b.Bars); err != nil {
		return domain.Receipt{}, err
	}
	if err := os.MkdirAll(a.Directory, 0755); err != nil {
		return domain.Receipt{}, err
	}
	path := filepath.Join(a.Directory, domain.FileName(b.Security, b.Category))
	old, err := ReadCSV(path)
	if err != nil && !os.IsNotExist(err) {
		return domain.Receipt{}, fmt.Errorf("已有历史文件不可读，保留原文件：%w", err)
	}
	if len(old) > 0 {
		if err := domain.ValidateBars(old); err != nil {
			return domain.Receipt{}, fmt.Errorf("已有历史文件质量异常：%w", err)
		}
	}
	// 第 2 步：统一市场时间与周期精度，按时间键合并，新数据覆盖重叠旧值，再升序排列。
	unique := make(map[string]domain.Bar, len(old)+len(b.Bars))
	// 顺序决定覆盖规则：先放旧记录，再用新记录覆盖同一市场时间键。
	for _, bars := range [][]domain.Bar{old, b.Bars} {
		for _, bar := range bars {
			bar.Date = bar.Date.In(ExchangeTime)
			// 非分钟周期统一到市场日期零点，避免同一天因时分不同被保存成多条记录。
			if !(b.Category < 4 || b.Category == 7 || b.Category == 8) {
				bar.Date = time.Date(bar.Date.Year(), bar.Date.Month(), bar.Date.Day(), 0, 0, 0, 0, ExchangeTime)
			}
			unique[bar.Date.Format("2006-01-02 15:04")] = bar
		}
	}
	merged := make([]domain.Bar, 0, len(unique))
	for _, bar := range unique {
		merged = append(merged, bar)
	}
	sort.Slice(merged, func(i, j int) bool { return merged[i].Date.Before(merged[j].Date) })
	// 第 3 步：只向同目录临时文件写完整合并结果；逐行观察取消，异常时清理临时文件。
	file, err := os.CreateTemp(a.Directory, ".bars-*.tmp")
	if err != nil {
		return domain.Receipt{}, err
	}
	tmp := file.Name()
	defer os.Remove(tmp)
	defer file.Close()
	hash := sha256.New()
	// 对实际输出的字节计算哈希，数据库读取归档时可确认拿到的正是这次提交的内容。
	writer := csv.NewWriter(io.MultiWriter(file, hash))
	err = writer.Write([]string{"date", "open", "high", "low", "close", "volume", "amount"})
	for _, bar := range merged {
		if err != nil {
			break
		}
		if err = ctx.Err(); err != nil {
			break
		}
		layout := "2006-01-02"
		if b.Category < 4 || b.Category == 7 || b.Category == 8 {
			layout = "2006-01-02 15:04"
		}
		record := []string{bar.Date.Format(layout)}
		// 不强制把“手”四舍五入为整数，95 股应保存为 0.95 手，避免奇数股成交量丢失。
		for _, v := range []float64{bar.Open, bar.High, bar.Low, bar.Close, bar.Volume, bar.Amount} {
			record = append(record, strconv.FormatFloat(v, 'f', -1, 64))
		}
		err = writer.Write(record)
	}
	// 第 4 步：Flush 处理 CSV 缓冲、Sync 同步文件，再 Close；三者成功才允许发布目标文件。
	writer.Flush()
	if err == nil {
		err = writer.Error()
	}
	if err == nil {
		err = file.Sync()
	}
	if closeErr := file.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return domain.Receipt{}, err
	}
	if err = ctx.Err(); err != nil {
		return domain.Receipt{}, err
	}
	// 第 5 步：再次确认没有取消后替换原 CSV；之前的任意失败都不会发布部分结果。
	if err = os.Rename(tmp, path); err != nil {
		return domain.Receipt{}, err
	}
	receipt := domain.Receipt{Path: path, Rows: len(merged), From: merged[0].Date.Format("2006-01-02 15:04"), To: merged[len(merged)-1].Date.Format("2006-01-02 15:04"), SHA256: hex.EncodeToString(hash.Sum(nil)), VolumeUnit: "lots_100_shares"}
	// 第 6 步：生成回执 sidecar。此时 CSV 已提交；sidecar 失败返回错误，但不撤销已发布 CSV。
	// 上层因此不能标记成功，下次可重新合并并幂等入库，不能声称两个文件属于同一事务。
	info, err := os.Stat(path)
	if err != nil {
		return domain.Receipt{}, err
	}
	receipt.Bytes = info.Size()
	metadata := struct {
		Schema     int            `json:"schema"`     // 归档 sidecar 的结构版本，独立于 DuckDB schema_migrations。
		Receipt    domain.Receipt `json:"receipt"`    // CSV 实际覆盖、单位、总条数和哈希。
		Source     string         `json:"source"`     // 最后提交该合并归档的处理来源，不宣称所有历史行都来自同一来源。
		UpdatedAt  time.Time      `json:"updatedAt"`  // 本次归档元数据更新时间，UTC。
		Adjustment string         `json:"adjustment"` // 固定 none，声明 CSV 保存的是未复权原始价。
	}{1, receipt, b.Source, time.Now().UTC(), "none"}
	if err = WriteJSON(path+".meta.json", metadata); err != nil {
		return domain.Receipt{}, err
	}
	return receipt, nil
}

// Latest 从有效归档中取最大时间，不依赖文件行序；文件缺失表示尚未建库。
// 已有文件损坏则明确报错，不能按“没有历史”继续下载并覆盖它。
func (a *CSVArchive) Latest(ctx context.Context, s domain.Security, category int) (time.Time, error) {
	if err := ctx.Err(); err != nil {
		return time.Time{}, err
	}
	bars, err := ReadCSV(filepath.Join(a.Directory, domain.FileName(s, category)))
	if os.IsNotExist(err) {
		return time.Time{}, nil
	}
	if err != nil {
		return time.Time{}, err
	}
	if err = domain.ValidateBars(bars); err != nil {
		return time.Time{}, err
	}
	var latest time.Time
	for _, b := range bars {
		if b.Date.After(latest) {
			latest = b.Date
		}
	}
	return latest, nil
}

// WriteJSON 为元数据和因子附件提供临时写入后替换，失败时清理临时文件。
func WriteJSON(path string, value any) error {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".json-*.tmp")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	defer f.Close()
	if _, err = f.Write(data); err == nil {
		err = f.Sync()
	}
	if closeErr := f.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	return os.Rename(f.Name(), path)
}
