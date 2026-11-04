package domain

import (
	"fmt"
	"math"
	"sort"
	"time"
)

// AdjustmentEvent 保存通达信 gbbq 原始事件；仅 Type=1 的分红送配事件参与当前复权计算。
type AdjustmentEvent struct {
	Date        int     `json:"date"`        // 事件生效日 YYYYMMDD 整数，调整的是严格早于此日的行情。
	Type        uint8   `json:"type"`        // gbbq 事件类型；其他类型仍可保存，但不套用 Type=1 的计算公式。
	Dividend    float64 `json:"dividend"`    // 每 10 股现金分红，元；计算时除以 10 换成每股现金项。
	RightsPrice float64 `json:"rightsPrice"` // 每股配股认购价格，元，不是每 10 股总认购金额。
	Bonus       float64 `json:"bonus"`       // 每 10 股送转股数，与配股数共同构成扩股倍数。
	Rights      float64 `json:"rights"`      // 每 10 股配股股数，与 RightsPrice 相乘得到每 10 股认购款。
}

// PriceFactor 是仿射变换系数：调整价 = 原始价 * Mul + Add。
// 现金分红产生平移项，因此不能只用一个乘法比例表示全部通达信事件。
type AdjustmentFactor struct {
	Mul float64 // 乘法系数，初始为 1；累计送转配股产生的价格缩放。
	Add float64 // 平移金额，元，初始为 0；累计现金分红和配股认购款的影响。
}

func dateNumber(date time.Time) int {
	return date.Year()*10000 + int(date.Month())*100 + date.Day()
}

// priceFactors 从最新行情向前累计每根 K 线的前复权系数，要求 bars 按时间升序。
// 事件只调整除权日前的价格；晚于序列末日的事件忽略，除权日当天不再次调整。
func priceFactors(bars []Bar, events []AdjustmentEvent) []AdjustmentFactor {
	// 第 1 步：以行情末日为计算观察边界，只保留 Type=1 且不晚于末日的事件，并按日期升序。
	factors := make([]AdjustmentFactor, len(bars))
	if len(bars) == 0 {
		return factors
	}
	latest := dateNumber(bars[len(bars)-1].Date)
	applicable := make([]AdjustmentEvent, 0, len(events))
	for _, event := range events {
		if event.Type == 1 && event.Date <= latest {
			applicable = append(applicable, event)
		}
	}
	sort.SliceStable(applicable, func(i, j int) bool { return applicable[i].Date < applicable[j].Date })
	// 第 2 步：从最新 K 线向过去扫描，两个游标各走一次；同一事件只会被合成一次。
	// mul/add 表示当前“原价→前复权价”的累计变换，最新端从恒等变换开始。
	mul, add := 1.0, 0.0
	eventIndex := len(applicable) - 1 // 指向尚未合成的最近一个历史事件，合成后递减。
	for i := len(bars) - 1; i >= 0; i-- {
		date := dateNumber(bars[i].Date)
		// 第 3 步：只有事件日在当前 K 线之后才调整它，除权日当天直接使用除权后的市场价格。
		for eventIndex >= 0 && applicable[eventIndex].Date > date {
			event := applicable[eventIndex]
			eventIndex--
			// 通达信事件按“每 10 股”记分红、送股和配股，先换算成每股的扩股倍数与现金项。
			// 单次变换为 (价格-c)/m；向过去累计时需按顺序合成乘法和平移，不能直接相加。
			m := (10 + event.Bonus + event.Rights) / 10
			c := (event.Dividend - event.Rights*event.RightsPrice) / 10
			if m <= 0 || math.IsNaN(m) || math.IsInf(m, 0) {
				continue
			}
			// 若已有变换是 M*p+A，再向过去跨一事件后变为 M*(p-c)/m+A。
			// 因此先更新 M=M/m，再更新 A=A-M*c；这里的先后顺序不能交换。
			mul /= m
			add -= mul * c
		}
		// 第 4 步：记录当前根的累计系数；到更早的 K 线时继续沿用，不重新扫描全部事件。
		factors[i] = AdjustmentFactor{Mul: mul, Add: add}
	}
	return factors
}

// adjustBars 复制输入后只变换 OHLC，成交量和成交额保留原值。
// 前复权以序列末端为基准，后复权再按首根系数归一化；都不修改调用方的原始数据。
func AdjustBars(bars []Bar, events []AdjustmentEvent, mode string) ([]Bar, error) {
	// 第 1 步：复制行情，raw 直接返回；校验 qfq/hfq 后，按完整输入序列计算系数。
	out := append([]Bar(nil), bars...)
	if mode == "" || mode == "raw" {
		return out, nil
	}
	if mode != "qfq" && mode != "hfq" {
		return nil, fmt.Errorf("未知复权方式 %q", mode)
	}
	if len(bars) == 0 {
		return out, nil
	}
	factors := priceFactors(bars, events)
	// 第 2 步：hfq 以序列首根为不变基准，把前复权系数整体作逆基准归一化。
	// (M_i*p+A_i-A_0)/M_0，故乘法和平移两部分都要除以首根乘数。
	if mode == "hfq" {
		base := factors[0]
		if base.Mul == 0 {
			return nil, fmt.Errorf("后复权基准系数无效")
		}
		for i := range factors {
			factors[i].Add = (factors[i].Add - base.Add) / base.Mul
			factors[i].Mul /= base.Mul
		}
	}
	// 第 3 步：在副本上将同一根的系数用于四个价格，量额保持原值，原始归档从未写回。
	for i := range out {
		factor := factors[i]
		out[i].Open = out[i].Open*factor.Mul + factor.Add
		out[i].High = out[i].High*factor.Mul + factor.Add
		out[i].Low = out[i].Low*factor.Mul + factor.Add
		out[i].Close = out[i].Close*factor.Mul + factor.Add
	}
	return out, nil
}

// FactorKey 为 gbbq 查找提供市场和代码的组合键，避免跨市场同码碰撞。
func FactorKey(market uint8, code string) string { return fmt.Sprintf("%d:%s", market, code) }
