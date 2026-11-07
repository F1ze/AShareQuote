package domain

import (
	"fmt"
	"strings"
)

type PreviewBar struct {
	Date   string  `json:"date"`   // 市场日期或日期时分，不附时区；非零时分格式为 YYYY-MM-DD HH:MM。
	Open   float64 `json:"open"`   // 开盘价，元，已按请求 Mode 在内存中处理。
	High   float64 `json:"high"`   // 最高价，元，与 Open 使用同一复权方式。
	Low    float64 `json:"low"`    // 最低价，元。
	Close  float64 `json:"close"`  // 收盘价，元。
	Volume float64 `json:"volume"` // CSV 成交量，手；与仓库 bars API 的 volume_shares（股）不同。
	Amount float64 `json:"amount"` // 原始成交额，元，不随复权模式变更。
}

// PreviewResponse 的 Total/From/To 描述整个归档；Bars 按请求返回完整序列或末尾窗口。
type PreviewResponse struct {
	File            string       `json:"file"`            // 经过校验的当前输出目录内行情文件名，不含目录。
	Mode            string       `json:"mode"`            // raw=原始价、qfq=前复权、hfq=后复权；缺省使用 raw。
	Total           int          `json:"total"`           // 截取 limit 前的完整行情条数，可能大于 len(Bars)。
	FactorAvailable bool         `json:"factorAvailable"` // 对应 gbbq CSV 是否存在；只检查存在性，不代表已校验内容或有 BaoStock 比例因子。
	FactorEvents    int          `json:"factorEvents"`    // 本请求实际读取的 gbbq 事件数；raw 模式不读取，可能为 0。
	Bars            []PreviewBar `json:"bars"`            // 升序排列；All 返回全部，否则默认 200 条、上限 800。
	From            string       `json:"from"`            // 完整序列最早市场时间，YYYY-MM-DD HH:MM，空序列为空串。
	To              string       `json:"to"`              // 完整序列最晚市场时间，不随 limit 缩短。
}

// PreviewQuery 只接受归档逻辑文件名，不能携带任意路径。
type PreviewQuery struct {
	All   bool   // 显式请求全部历史；复权仍基于完整序列计算。
	File  string // 原始行情 CSV 文件名，不含目录，不允许因子文件。
	Mode  string // raw/qfq/hfq，空值默认 raw。
	Limit int    // 尾部显示条数，0 默认 200，允许 1–800。
}

func (q PreviewQuery) Normalize() (PreviewQuery, error) {
	if q.File == "" || strings.ContainsAny(q.File, "/\\") || !strings.HasSuffix(q.File, ".csv") || strings.HasSuffix(q.File, "_gbbq.csv") {
		return q, fmt.Errorf("%w：预览文件名无效", ErrInvalidInput)
	}
	if q.Mode == "" {
		q.Mode = "raw"
	}
	if q.Mode != "raw" && q.Mode != "qfq" && q.Mode != "hfq" {
		return q, fmt.Errorf("%w：未知复权方式", ErrInvalidInput)
	}
	if q.Limit == 0 {
		q.Limit = 200
	}
	if q.Limit < 1 || q.Limit > 800 {
		return q, fmt.Errorf("%w：预览条数须为 1–800", ErrInvalidInput)
	}
	return q, nil
}
