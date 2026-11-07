package repository

import (
	"context"
	"encoding/binary"
	"encoding/csv"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"quotes/internal/domain"
)

func factorKey(market uint8, code string) string {
	return fmt.Sprintf("%d:%s", market, code)
}

func LoadGBBQ(path string) (map[string][]domain.AdjustmentEvent, error) {
	// 流程：读头部记录数 → 校验至少有 total×29 字节 → 解码各记录 → 按市场代码分组 → 事件升序。
	// 每条记录布局：0–3 日期，4 类型，5 市场，6–11 代码，12–27 四个 float32 数值，末字节保留。
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("读取复权因子：%w", err)
	}
	if len(raw) < 4 {
		return nil, fmt.Errorf("复权因子文件过短")
	}
	total := int(binary.LittleEndian.Uint32(raw[:4]))
	if total < 0 || total > 10_000_000 || 4+total*29 > len(raw) {
		return nil, fmt.Errorf("复权因子文件长度不符：记录数 %d，文件 %d 字节", total, len(raw))
	}
	factors := make(map[string][]domain.AdjustmentEvent)
	for i := 0; i < total; i++ {
		offset := 4 + i*29
		record := raw[offset : offset+29]
		code := strings.TrimRight(string(record[6:12]), "\x00")
		event := domain.AdjustmentEvent{
			Date:        int(binary.LittleEndian.Uint32(record[:4])),
			Type:        record[4],
			Dividend:    float64(math.Float32frombits(binary.LittleEndian.Uint32(record[12:16]))),
			RightsPrice: float64(math.Float32frombits(binary.LittleEndian.Uint32(record[16:20]))),
			Bonus:       float64(math.Float32frombits(binary.LittleEndian.Uint32(record[20:24]))),
			Rights:      float64(math.Float32frombits(binary.LittleEndian.Uint32(record[24:28]))),
		}
		key := factorKey(record[5], code)
		factors[key] = append(factors[key], event)
	}
	for key := range factors {
		sort.SliceStable(factors[key], func(i, j int) bool { return factors[key][i].Date < factors[key][j].Date })
	}
	return factors, nil
}

func adjustmentFileName(security domain.Security) string {
	return fmt.Sprintf("%s%s_gbbq.csv", domain.MarketPrefix(security.Market), security.Code)
}

func WriteAdjustmentCSV(output string, security domain.Security, events []domain.AdjustmentEvent) error {
	finalPath := filepath.Join(output, adjustmentFileName(security))
	if len(events) == 0 {
		if err := os.Remove(finalPath); err != nil && !os.IsNotExist(err) {
			return err
		}
		return nil
	}
	file, err := os.CreateTemp(output, ".gbbq-*.tmp")
	if err != nil {
		return err
	}
	tempPath := file.Name()
	ok := false
	defer func() {
		_ = file.Close()
		if !ok {
			_ = os.Remove(tempPath)
		}
	}()
	writer := csv.NewWriter(file)
	if err = writer.Write([]string{"date", "type", "dividend", "rights_price", "bonus", "rights"}); err == nil {
		for _, event := range events {
			record := []string{
				strconv.Itoa(event.Date), strconv.Itoa(int(event.Type)),
				strconv.FormatFloat(event.Dividend, 'f', 6, 64), strconv.FormatFloat(event.RightsPrice, 'f', 6, 64),
				strconv.FormatFloat(event.Bonus, 'f', 6, 64), strconv.FormatFloat(event.Rights, 'f', 6, 64),
			}
			if err = writer.Write(record); err != nil {
				break
			}
		}
	}
	writer.Flush()
	if err == nil {
		err = writer.Error()
	}
	if closeErr := file.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	if err = os.Rename(tempPath, finalPath); err != nil {
		return err
	}
	ok = true
	return nil
}

func ReadAdjustmentCSV(path string) ([]domain.AdjustmentEvent, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	reader := csv.NewReader(file)
	records, err := reader.ReadAll()
	if err != nil {
		return nil, err
	}
	events := make([]domain.AdjustmentEvent, 0, len(records))
	for i, record := range records {
		if i == 0 || len(record) < 6 {
			continue
		}
		date, err := strconv.Atoi(record[0])
		if err != nil {
			return nil, fmt.Errorf("复权日期无效：%q", record[0])
		}
		eventType, err := strconv.Atoi(record[1])
		if err != nil {
			return nil, fmt.Errorf("复权类型无效：%q", record[1])
		}
		values := make([]float64, 4)
		for j := range values {
			values[j], err = strconv.ParseFloat(record[j+2], 64)
			if err != nil {
				return nil, fmt.Errorf("复权数值无效：%q", record[j+2])
			}
		}
		events = append(events, domain.AdjustmentEvent{Date: date, Type: uint8(eventType), Dividend: values[0], RightsPrice: values[1], Bonus: values[2], Rights: values[3]})
	}
	sort.SliceStable(events, func(i, j int) bool { return events[i].Date < events[j].Date })
	return events, nil
}

func ReadBarsCSV(path string) ([]domain.Bar, error) {
	return ReadBarsCSVContext(context.Background(), path)
}

// ReadBarsCSVContext 逐行转换，避免同时保存全部 CSV 字符串；取消请求后及时释放文件。
func ReadBarsCSVContext(ctx context.Context, path string) ([]domain.Bar, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	reader := csv.NewReader(file)
	bars := make([]domain.Bar, 0)
	for i := 0; ; i++ {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		record, err := reader.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}
		if i == 0 || len(record) < 7 {
			continue
		}
		layout := "2006-01-02"
		if strings.Contains(record[0], " ") {
			layout = "2006-01-02 15:04"
		}
		date, err := time.ParseInLocation(layout, record[0], domain.ExchangeTime)
		if err != nil {
			return nil, fmt.Errorf("K 线日期无效：%q", record[0])
		}
		values := make([]float64, 6)
		for j := range values {
			values[j], err = strconv.ParseFloat(record[j+1], 64)
			if err != nil {
				return nil, fmt.Errorf("K 线数值无效：%q", record[j+1])
			}
		}
		bars = append(bars, domain.Bar{Date: date, Open: values[0], High: values[1], Low: values[2], Close: values[3], Volume: values[4], Amount: values[5]})
	}
	return bars, nil
}
