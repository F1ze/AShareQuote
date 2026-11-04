package service

import (
	"path/filepath"

	"quotes/internal/domain"
)

func marketPrefix(market uint8) string { return domain.MarketPrefix(market) }
func categoryName(category int) string { return domain.CategoryName(category) }

func receiptPath(directory, name string) string { return filepath.Join(directory, name) }
