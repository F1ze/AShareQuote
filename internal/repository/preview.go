package repository

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"quotes/internal/domain"
)

func (a *CSVArchive) ReadBars(ctx context.Context, name string) ([]domain.Bar, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	bars, err := ReadBarsCSV(filepath.Join(a.Directory, name))
	if os.IsNotExist(err) {
		return nil, fmt.Errorf("%w：读取行情预览：%v", domain.ErrNotFound, err)
	}
	return bars, err
}
func (a *CSVArchive) ReadAdjustment(ctx context.Context, name string, load bool) ([]domain.AdjustmentEvent, bool, error) {
	if err := ctx.Err(); err != nil {
		return nil, false, err
	}
	stem := strings.SplitN(name, "_", 2)[0]
	path := filepath.Join(a.Directory, stem+"_gbbq.csv")
	_, err := os.Stat(path)
	if os.IsNotExist(err) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	if !load {
		return nil, true, nil
	}
	events, err := ReadAdjustmentCSV(path)
	return events, true, err
}
