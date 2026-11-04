// Package historyfile 提供本地历史数据读取，不受在线接口单页和历史偏移限制。
// 解码器按扩展名注册，并将各格式的价格、时间与成交量统一为归档约定。
package historyfile

import (
	"context"
	"encoding/binary"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"quotes/internal/domain"
	"quotes/internal/repository"
)

// Decoder 只负责文件解码与基础质量校验；目录扫描、任务恢复和入库由其他层组织。
type Decoder interface {
	Read(context.Context, string, string) ([]domain.Bar, error)
}

// Source 用格式注册表分派解码器，新增格式无需修改任务导入流水线。
type Source struct {
	Readers map[string]Decoder // 小写扩展名（含点）→解码器，如 .day/.lc1/.lc5/.csv；启动后只读。
}
type csvDecoder struct{}
type tdxDecoder struct {
	minute bool // false=.day 的整数分价格/日期；true=.lc1/.lc5 的 float32 价格/压缩日期。
}

var filePattern = regexp.MustCompile(`^(sh|sz|bj)([0-9]{6})(?:_(day|1m|5m|15m|30m|60m|week|month|quarter|year))?\.(csv|day|lc1|lc5)$`)
var categories = map[string]int{"day": 9, "1m": 7, "5m": 0, "15m": 1, "30m": 2, "60m": 3, "week": 5, "month": 6, "quarter": 10, "year": 11}

func New() *Source {
	return &Source{Readers: map[string]Decoder{".csv": csvDecoder{}, ".day": tdxDecoder{}, ".lc1": tdxDecoder{minute: true}, ".lc5": tdxDecoder{minute: true}}}
}

// Discover 从规范文件名识别市场、证券与周期，仅收集支持的 A 股文件。
// 不跟随符号链接，也跳过依赖目录，避免递归扫描无关路径；CSV 必须在文件名中声明周期。
func (s *Source) Discover(ctx context.Context, directory string) ([]domain.Input, error) {
	// 第 1 步：要求目录存在，再递归扫描；访问错误或取消直接返回，不把不完整清单当作完整计划。
	info, err := os.Stat(directory)
	if err != nil {
		return nil, err
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("请选择历史数据所在目录")
	}
	var out []domain.Input
	err = filepath.WalkDir(directory, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if entry.IsDir() {
			if entry.Name() == ".git" || entry.Name() == ".qflux" || entry.Name() == "node_modules" || entry.Name() == ".venv" {
				return filepath.SkipDir
			}
			return nil
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return nil
		}
		// 第 2 步：只匹配约定文件名，从中识别市场和代码；不打开无关文件猜测其内容。
		match := filePattern.FindStringSubmatch(strings.ToLower(entry.Name()))
		if match == nil {
			return nil
		}
		market := map[string]uint8{"sz": 0, "sh": 1, "bj": 2}[match[1]]
		security := domain.Security{Market: market, Code: match[2]}
		if !domain.IsAShare(security) {
			return nil
		}
		// 第 3 步：二进制格式由扩展名确定周期；CSV 必须显式带 _day/_1m 等周期后缀。
		category := 9
		switch match[4] {
		case "csv":
			var ok bool
			category, ok = categories[match[3]]
			if !ok {
				return fmt.Errorf("CSV 文件名必须带周期，例如 sh600000_day.csv")
			}
		case "lc1":
			category = 7
		case "lc5":
			category = 0
		}
		// 第 4 步：仅收集输入元信息，真正解码、校验和入库由 runLocal 按计划逐项执行。
		out = append(out, domain.Input{Security: security, Category: category, Path: path})
		return nil
	})
	if err != nil {
		return nil, err
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("目录中没有 A 股历史文件；支持 sh600000.day、.lc1、.lc5 和 sh600000_day.csv 等规范文件名")
	}
	return out, nil
}

func (s *Source) Read(ctx context.Context, input domain.Input, unit string) ([]domain.Bar, error) {
	decoder, ok := s.Readers[strings.ToLower(filepath.Ext(input.Path))]
	if !ok {
		return nil, fmt.Errorf("不支持的历史文件格式")
	}
	return decoder.Read(ctx, input.Path, unit)
}

func (csvDecoder) Read(ctx context.Context, path, unit string) ([]domain.Bar, error) {
	if unit != "lots" && unit != "shares" {
		return nil, fmt.Errorf("CSV 必须明确成交量单位：lots 或 shares")
	}
	bars, err := repository.ReadCSV(path)
	if err != nil {
		return nil, err
	}
	for i := range bars {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		// domain.Bar/标准 CSV 使用手，输入为股时先除以 100；允许小数手，不能截断奇数股。
		if unit == "shares" {
			bars[i].Volume /= 100
		}
	}
	return bars, domain.ValidateBars(bars)
}

// Read 的流程是：每次读满 32 字节 → 按日线/分钟布局解时间和 OHLC → 统一量额 → 整批校验。
// 原生 TDX 股票文件成交量本来就是股，因此忽略仅供外部 CSV 使用的单位参数。
func (d tdxDecoder) Read(ctx context.Context, path, _ string) ([]domain.Bar, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var out []domain.Bar
	// 原生日线与分钟线均为 32 字节定长记录，但日期和价格编码不同。
	// 文件尾若不足一条记录必须报错，不能把截断文件当作完整历史导入。
	raw := make([]byte, 32)
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		_, err := io.ReadFull(f, raw)
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("TDX 文件不是完整的 32 字节记录：%w", err)
		}
		var date time.Time
		prices := make([]float64, 4)
		// 第 1 分支：分钟日期占 2+2 字节，价格为 float32 元；检查 time.Date 是否把非法日期自动归一化。
		if d.minute {
			day, minute := int(binary.LittleEndian.Uint16(raw)), int(binary.LittleEndian.Uint16(raw[2:]))
			// 日期高位存相对 2004 年的偏移，低位存月日；第二个字段为零点起的分钟数。
			year, month, dom := day/2048+2004, day%2048/100, day%2048%100
			date = time.Date(year, time.Month(month), dom, minute/60, minute%60, 0, 0, domain.ExchangeTime)
			if int(date.Month()) != month || date.Day() != dom || minute >= 1440 {
				return nil, fmt.Errorf("TDX 分钟日期无效")
			}
			for i := range prices {
				prices[i] = float64(math.Float32frombits(binary.LittleEndian.Uint32(raw[4+i*4:])))
			}
		} else {
			// 第 2 分支：日线日期为 uint32 的 YYYYMMDD，四个价格是整数分，使用独立规则解码。
			ymd := binary.LittleEndian.Uint32(raw)
			value := fmt.Sprintf("%08d", ymd)
			date, err = time.ParseInLocation("20060102", value, domain.ExchangeTime)
			if err != nil {
				return nil, err
			}
			// 日线价格为整数分；分钟线分支使用 float32 元，不能共用同一价格解码规则。
			for i := range prices {
				prices[i] = float64(binary.LittleEndian.Uint32(raw[4+i*4:])) / 100
			}
		}
		out = append(out, domain.Bar{Date: date, Open: prices[0], High: prices[1], Low: prices[2], Close: prices[3], Amount: float64(math.Float32frombits(binary.LittleEndian.Uint32(raw[20:]))), Volume: float64(binary.LittleEndian.Uint32(raw[24:])) / 100})
	}
	// 全部读取后再验证 OHLC/有限值；遇到半条记录或无效行时不返回可用于保存的“部分成功”。
	return out, domain.ValidateBars(out)
}
