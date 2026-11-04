//go:build windows

// Package processlock 保护跨进程的单写者应用状态；非正常退出后锁也由操作系统释放。
package processlock

import (
	"fmt"
	"os"

	"golang.org/x/sys/windows"
)

// Acquire 通过零共享模式打开锁文件；是否持锁由打开的句柄决定，不由文件是否存在决定。
// 调用方必须持有返回文件到应用退出，关闭句柄即释放锁，无需删除锁文件。
func Acquire(path string) (*os.File, error) {
	name, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return nil, err
	}
	handle, err := windows.CreateFile(name, windows.GENERIC_READ|windows.GENERIC_WRITE, 0, nil, windows.OPEN_ALWAYS, windows.FILE_ATTRIBUTE_NORMAL, 0)
	if err != nil {
		return nil, fmt.Errorf("该数据目录正在被其他 QFLUX 进程使用，请使用已有服务或先关闭它：%w", err)
	}
	return os.NewFile(uintptr(handle), path), nil
}
