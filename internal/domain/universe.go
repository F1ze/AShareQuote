package domain

// codeRange 表示六位证券代码的闭区间；两端保持等长数字文本，保留前导零。
type codeRange struct {
	first string // 包含的起始代码。
	last  string // 包含的结束代码。
}

// aShareCodeRanges 是只读分类规则，不是已上市证券名单。
// 深市区间依据交易所代码区间表；沪市保留主板与科创板普通股号段。
// https://www.szse.cn/marketServices/technicalservice/doc/P020241212550140892927.pdf
var aShareCodeRanges = map[uint8][]codeRange{
	0: { // 深圳：主板与创业板；显式避开 001001–001199、309800–309999 存托凭证。
		{"000001", "001000"},
		{"001200", "004999"},
		{"300000", "309799"},
	},
	1: { // 上海：主板 600/601/603/605，科创板 688；689 为存托凭证。
		{"600000", "601999"},
		{"603000", "603999"},
		{"605000", "605999"},
		{"688000", "688999"},
	},
	2: { // 北京：兼容历史普通股旧码及 920 新码，889 发行申购码不属于行情股票。
		{"430000", "439999"},
		{"830000", "839999"},
		{"870000", "879999"},
		{"880000", "888999"},
		{"920000", "920999"},
	},
}

// IsAShare 按交易所的 A 股代码范围分类，排除指数、基金、债券和 B 股等其他品种。
// 它只识别候选号段，不证明代码已经发行、历史上市归属或当前可交易状态。
// 北京旧码与股转系统存在重叠，具体上市身份仍须来源名录确认，不能由此自动合并新旧代码。
func IsAShare(s Security) bool {
	return inCodeRanges(s.Code, aShareCodeRanges[s.Market])
}

// ETF 与可转债使用交易代码号段，只判断支持范围，不推断证券是否已上市。
var etfCodeRanges = map[uint8][]codeRange{
	MarketSZ: {{"158000", "159899"}, {"159901", "159999"}}, // 159900 是申赎现金替代虚拟证券。
	MarketSH: {{"510000", "513999"}, {"515000", "518999"}, {"560000", "563999"}, {"588000", "589999"}},
}

var convertibleCodeRanges = map[uint8][]codeRange{
	MarketSZ: {{"123000", "123999"}, {"127000", "128999"}},
	MarketSH: {{"110000", "111999"}, {"113000", "113999"}, {"118000", "118999"}},
}

func IsETF(s Security) bool { return inCodeRanges(s.Code, etfCodeRanges[s.Market]) }

func IsConvertibleBond(s Security) bool { return inCodeRanges(s.Code, convertibleCodeRanges[s.Market]) }

func IsTDXInstrument(s Security) bool { return IsAShare(s) || IsETF(s) || IsConvertibleBond(s) }

func inCodeRanges(code string, ranges []codeRange) bool {
	// 1. 只接受规范化后的六位 ASCII 数字，不在分类器中猜市场、补零或清理输入。
	if len(code) != 6 {
		return false
	}
	for i := 0; i < len(code); i++ {
		if code[i] < '0' || code[i] > '9' {
			return false
		}
	}
	// 2. 按市场查询闭区间；等长 ASCII 数字的字典序等于数值序，无需转整数。
	// 未知市场查到空规则，自然拒绝；名称、ST 或退市状态不参与号段分类。
	for _, r := range ranges {
		if code >= r.first && code <= r.last {
			return true
		}
	}
	return false
}
