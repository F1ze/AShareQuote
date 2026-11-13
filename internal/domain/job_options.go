package domain

import (
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"
)

// DownloadDateRange 将界面上“包含起止当天”的范围转换成 [开始日零点, 结束日次日零点)。
// 使用北京时间解析，保证分钟线不会漏掉结束日，也不会随服务器所在时区改变范围。
func DownloadDateRange(options DownloadOptions, now time.Time) (time.Time, time.Time, error) {
	from, err := time.ParseInLocation("2006-01-02", options.StartDate, ExchangeTime)
	if err != nil {
		return time.Time{}, time.Time{}, errors.New("请选择有效的开始日期")
	}
	to, err := time.ParseInLocation("2006-01-02", options.EndDate, ExchangeTime)
	if err != nil {
		return time.Time{}, time.Time{}, errors.New("请选择有效的结束日期")
	}
	if from.After(to) {
		return time.Time{}, time.Time{}, errors.New("开始日期不能晚于结束日期")
	}
	if to.Format("2006-01-02") > now.In(ExchangeTime).Format("2006-01-02") {
		return time.Time{}, time.Time{}, errors.New("结束日期不能晚于今天（北京时间）")
	}
	return from, to.AddDate(0, 0, 1), nil
}

// MaxHistoricOffset 是当前通达信协议可表达的最大历史位置，不允许转 uint16 时回绕。
const MaxHistoricOffset = 65535

const (
	DefaultWorkers     = 32 // 网络等待型下载的默认并发数；实测与 40 相当，且比更高并发更节省连接资源。
	DefaultRequestRate = 40 // 应用内通达信连接与业务请求的默认全局启动频率，次/秒。
)

// NormalizeDownloadOptions 只处理业务默认值与输入约束，不探测服务、目录或数据库。
// 来源是否注册、入库能力是否可用由服务根据注入依赖判断。
func NormalizeDownloadOptions(o DownloadOptions, defaultOutput string, now time.Time) (DownloadOptions, error) {
	if o.OutputDir == "" {
		o.OutputDir = defaultOutput
	}
	// 0 是合法 5 分钟周期，只有完全缺省的旧请求才兼容为日线。
	if o.Category == 0 && o.Count == 0 && o.StartDate == "" {
		o.Category = 9
	}
	if o.Count == 0 {
		o.Count = 800
	}
	if o.Workers == 0 {
		o.Workers = DefaultWorkers
	}
	if o.Timeout == 0 {
		o.Timeout = 8
	}
	if o.RequestRate == 0 {
		o.RequestRate = DefaultRequestRate
	}
	if o.RequestRate < 1 || o.RequestRate > 200 {
		return o, errors.New("全局请求频率须为 1–200 次/秒")
	}
	if o.Source == "" {
		o.Source = "tdx"
	}
	if o.Storage != "" && o.Storage != "csv" && o.Storage != "duckdb" {
		return o, errors.New("存储类型无效")
	}
	if o.Category < 0 || o.Category > 12 {
		return o, errors.New("行情周期无效")
	}
	if o.Incremental && o.StartDate == "" {
		return o, errors.New("增量更新需要明确的日期范围")
	}
	if o.StartDate != "" || o.EndDate != "" {
		if _, _, err := DownloadDateRange(o, now); err != nil {
			return o, err
		}
		o.Start = 0
	} else if o.Start < 0 || o.Start > MaxHistoricOffset || o.Count < 1 || o.Count > MaxHistoricOffset+1-o.Start {
		return o, errors.New("历史位置须为 0–65535，下载数量不得超出协议可访问范围")
	}
	if o.Workers < 1 || o.Workers > 64 || o.Timeout < 2 || o.Timeout > 60 {
		return o, errors.New("并发数须为 1–64，超时须为 2–60 秒")
	}
	if o.ImportDir != "" && o.CSVVolumeUnit != "shares" && o.CSVVolumeUnit != "lots" {
		return o, errors.New("请选择 CSV 成交量单位（shares 或 lots）")
	}
	if o.ImportDir == "" && len(o.Markets) == 0 && strings.TrimSpace(o.Codes) == "" {
		return o, errors.New("请至少选择一个市场或填写证券代码")
	}
	seen := map[int]bool{}
	for _, market := range o.Markets {
		if market < 0 || market > 2 || seen[market] {
			return o, errors.New("市场选择无效")
		}
		seen[market] = true
	}
	if strings.TrimSpace(o.Codes) != "" {
		codes, err := ParseCodes(o.Codes)
		if err != nil {
			return o, err
		}
		if o.ASharesOnly {
			supported := IsAShare
			if o.Source == "tdx" && o.ImportDir == "" {
				supported = IsTDXInstrument
			}
			for _, security := range codes {
				if !supported(security) {
					return o, fmt.Errorf("%s%s 不属于该来源支持的证券代码范围", MarketPrefix(security.Market), security.Code)
				}
			}
		}
	}
	return o, nil
}
func ParseCodes(value string) ([]Security, error) {
	items := strings.FieldsFunc(value, func(r rune) bool {
		return r == ',' || r == '，' || r == ';' || r == '；' || r == '、' || r == '\n' || r == '\r' || r == '\t' || r == ' '
	})
	seen := make(map[string]bool)
	out := make([]Security, 0, len(items))
	for _, item := range items {
		item = strings.ToLower(strings.TrimSpace(item))
		market := uint8(255)
		for _, candidate := range []struct {
			market uint8  // 显式代码前缀对应的市场编号，优先于按代码首位推测市场。
			prefix string // 用户输入允许的 sh/sz/bj 前缀，识别后从六位代码中去掉。
		}{{0, "sz"}, {1, "sh"}, {2, "bj"}} {
			if strings.HasPrefix(item, candidate.prefix) {
				market = candidate.market
				item = strings.TrimPrefix(item, candidate.prefix)
				break
			}
		}
		if len(item) != 6 {
			return nil, fmt.Errorf("证券代码 %q 无效，应为 6 位数字", item)
		}
		if strings.IndexFunc(item, func(r rune) bool { return r < '0' || r > '9' }) >= 0 {
			return nil, fmt.Errorf("证券代码 %q 无效，应为 6 位数字", item)
		}
		if market == 255 {
			switch item[0] {
			case '1':
				market = MarketSZ
				if strings.HasPrefix(item, "11") {
					market = MarketSH
				}
			case '5', '6', '9':
				market = 1
			case '4', '8':
				market = 2
			default:
				market = 0
			}
			if strings.HasPrefix(item, "920") {
				market = 2
			}
		}
		key := fmt.Sprintf("%d:%s", market, item)
		if !seen[key] {
			seen[key] = true
			out = append(out, Security{Market: market, Code: item})
		}
	}
	if len(out) == 0 {
		return nil, errors.New("请至少填写一个证券代码")
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Market == out[j].Market {
			return out[i].Code < out[j].Code
		}
		return out[i].Market < out[j].Market
	})
	return out, nil
}
