package domain

import (
	"fmt"
	"math"
)

// Validate 拒绝空批次、无效时间、非有限值和不成立的 OHLC 关系。
// 零成交量可能是合法记录，但零价格不能作为缺失行情的占位值写入。
func ValidateBars(bars []Bar) error {
	if len(bars) == 0 {
		return fmt.Errorf("没有可保存的行情")
	}
	for i, b := range bars {
		for _, v := range []float64{b.Open, b.High, b.Low, b.Close, b.Volume, b.Amount} {
			if math.IsNaN(v) || math.IsInf(v, 0) || v < 0 {
				return fmt.Errorf("第 %d 条行情包含负数或非有限值", i+1)
			}
		}
		if b.Date.IsZero() || b.Open <= 0 || b.Close <= 0 || b.Low <= 0 || b.High < math.Max(b.Open, b.Close) || b.Low > math.Min(b.Open, b.Close) || b.High < b.Low {
			return fmt.Errorf("第 %d 条行情日期或 OHLC 关系异常", i+1)
		}
	}
	return nil
}
