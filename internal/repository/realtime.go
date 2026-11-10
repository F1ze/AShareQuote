package repository

import (
	"context"
	"strings"

	"gorm.io/gorm"

	"quotes/internal/domain"
)

// SecurityNames 只查询已存证券参考信息；空名称保持未知，不触发全市场码表下载。
func (d *DuckDB) SecurityNames(ctx context.Context, stocks []domain.Security) (map[string]string, error) {
	return transact(ctx, d, func(tx *gorm.DB) (map[string]string, error) {
		names := map[string]string{}
		if len(stocks) == 0 {
			return names, nil
		}
		query := tx.Model(&instrumentRecord{})
		for i, s := range stocks {
			condition := &instrumentRecord{Exchange: strings.ToUpper(domain.MarketPrefix(s.Market)), Code: s.Code}
			if i == 0 {
				query = query.Where(condition)
			} else {
				query = query.Or(condition)
			}
		}
		var records []datasetInstrument
		if err := query.Select("exchange,code,name").Find(&records).Error; err != nil {
			return nil, err
		}
		for _, r := range records {
			if r.Name != nil {
				names[strings.ToLower(r.Exchange)+r.Code] = *r.Name
			}
		}
		return names, nil
	})
}
