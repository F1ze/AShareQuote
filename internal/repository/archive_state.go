package repository

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"quotes/internal/domain"
)

// Prepare 将创建目录的技术细节留在仓库，服务只决定何时准备本次输出位置。
func (a *CSVArchive) Prepare() error { return os.MkdirAll(a.Directory, 0755) }

func fileHash(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	hash := sha256.New()
	if _, err = io.Copy(hash, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

// State 读当前归档及已存在附件的哈希；缺失和无法读取不是同一种状态。
// 调用方在入库后、写成功断点前使用它，避免将已被外部修改的文件标成已完成。
func (a *CSVArchive) State(ctx context.Context, name string) (domain.ArchiveState, error) {
	if err := ctx.Err(); err != nil {
		return domain.ArchiveState{}, err
	}
	if name == "" || filepath.Base(name) != name {
		return domain.ArchiveState{}, fmt.Errorf("归档文件名无效")
	}
	path := filepath.Join(a.Directory, name)
	digest, err := fileHash(path)
	if err != nil {
		return domain.ArchiveState{}, err
	}
	info, err := os.Stat(path)
	if err != nil {
		return domain.ArchiveState{}, err
	}
	state := domain.ArchiveState{SHA256: digest, Bytes: info.Size(), Attachments: map[string]string{}}
	stem := strings.SplitN(name, "_", 2)[0]
	for _, attachment := range []string{stem + "_gbbq.csv", stem + "_baostock_factors.json"} {
		if err := ctx.Err(); err != nil {
			return domain.ArchiveState{}, err
		}
		hash, err := fileHash(filepath.Join(a.Directory, attachment))
		if err == nil {
			state.Attachments[attachment] = hash
		} else if !os.IsNotExist(err) {
			return domain.ArchiveState{}, err
		}
	}
	return state, nil
}

// Verify 按断点记录校验整组资源；只校验当时记录的附件，不将后添加的文件误当作丢失。
func (a *CSVArchive) Verify(ctx context.Context, c domain.Checkpoint) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	if filepath.Base(c.File.Name) != c.File.Name || c.File.Name == "" {
		return false, nil
	}
	digest, err := fileHash(filepath.Join(a.Directory, c.File.Name))
	if os.IsNotExist(err) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if digest != c.SHA256 {
		return false, nil
	}
	for name, want := range c.Attachments {
		if filepath.Base(name) != name {
			return false, nil
		}
		if err := ctx.Err(); err != nil {
			return false, err
		}
		actual, err := fileHash(filepath.Join(a.Directory, name))
		if os.IsNotExist(err) {
			return false, nil
		}
		if err != nil {
			return false, err
		}
		if actual != want {
			return false, nil
		}
	}
	return true, nil
}

// FileFactors 只读写原始因子文件，不负责选择来源或计算复权价。
type FileFactors struct{}

func (FileFactors) LoadGBBQ(ctx context.Context, path string) (map[string][]domain.AdjustmentEvent, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return LoadGBBQ(path)
}
func (FileFactors) SaveAdjustment(ctx context.Context, output string, security domain.Security, events []domain.AdjustmentEvent) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return WriteAdjustmentCSV(output, security, events)
}
func (FileFactors) SaveProviderFactors(ctx context.Context, output string, security domain.Security, factors []domain.PriceFactor) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return WriteJSON(filepath.Join(output, domain.MarketPrefix(security.Market)+security.Code+"_baostock_factors.json"), factors)
}
