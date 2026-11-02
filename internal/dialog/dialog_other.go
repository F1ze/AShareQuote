//go:build !windows

package dialog

import (
	"context"
	"fmt"
)

func pickPath(_ context.Context, _ string) (string, error) {
	return "", fmt.Errorf("当前系统暂不支持原生路径选择框，请手动输入完整路径")
}
