package download

import "quotes/internal/core"

func marketPrefix(market uint8) string { return core.MarketPrefix(market) }
func categoryName(category int) string { return core.CategoryName(category) }
