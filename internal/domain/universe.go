// Package universe 负责证券范围筛选，与数据传输方式无关。
package domain

import (
	"strings"
)

// IsAShare 按交易所的 A 股代码范围分类，排除指数、基金、债券和 B 股等其他品种。
// 命中范围只说明代码类别，不能证明该证券的上市/退市时间或当前是否可交易。
func IsAShare(s Security) bool {
	if len(s.Code) != 6 || strings.IndexFunc(s.Code, func(r rune) bool { return r < '0' || r > '9' }) >= 0 {
		return false
	}
	switch s.Market {
	case 0:
		return strings.HasPrefix(s.Code, "000") || strings.HasPrefix(s.Code, "001") || strings.HasPrefix(s.Code, "002") || strings.HasPrefix(s.Code, "003") || strings.HasPrefix(s.Code, "300") || strings.HasPrefix(s.Code, "301")
	case 1:
		return strings.HasPrefix(s.Code, "600") || strings.HasPrefix(s.Code, "601") || strings.HasPrefix(s.Code, "603") || strings.HasPrefix(s.Code, "605") || strings.HasPrefix(s.Code, "688")
	case 2:
		// 保留历史旧码与 920 新码，排除 889 申购码；此处不推断旧码与新码的公司身份映射。
		return strings.HasPrefix(s.Code, "43") || strings.HasPrefix(s.Code, "83") || strings.HasPrefix(s.Code, "87") || (strings.HasPrefix(s.Code, "88") && !strings.HasPrefix(s.Code, "889")) || strings.HasPrefix(s.Code, "920")
	}
	return false
}
