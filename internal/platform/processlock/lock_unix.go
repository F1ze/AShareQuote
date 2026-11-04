//go:build !windows

package processlock

import (
	"fmt"
	"os"
	"syscall"
)

// Acquire 使用非阻塞排他锁，已有写者时立即失败，避免第二个应用一直等待。
// 锁跟随打开的文件描述符，调用方关闭返回文件或进程退出时自动释放。
func Acquire(path string) (*os.File, error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, err
	}
	if err = syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		f.Close()
		return nil, fmt.Errorf("数据目录正在被其他 QFLUX 进程使用：%w", err)
	}
	return f, nil
}
