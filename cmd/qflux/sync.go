package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"time"

	"quotes/internal/domain"
	"quotes/internal/service"
)

// runSync 只负责无界面生命周期和退出码，不另写一套采集逻辑。
// 只有完全成功才返回 nil；部分失败或覆盖警告也返回错误，便于系统计划任务发现需处理事项。
func runSync(ctx context.Context, manager *service.JobService, options domain.DownloadOptions, resume string) error {
	var task domain.TaskSnapshot
	var err error
	if resume != "" {
		task, err = manager.Resume(resume)
	} else {
		task, err = manager.Start(options)
	}
	if err != nil {
		return err
	}
	fmt.Printf("任务 %s 已启动\n", task.ID)
	timer := time.NewTicker(time.Second)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			_ = manager.Cancel()
			return ctx.Err()
		case <-timer.C:
			s := manager.Snapshot()
			if s == nil {
				return fmt.Errorf("任务状态丢失")
			}
			if s.State == "preparing" || s.State == "running" || s.State == "cancelling" {
				continue
			}
			_ = json.NewEncoder(os.Stdout).Encode(s)
			if s.State != "completed" {
				return fmt.Errorf("任务需要处理：%s（%s）", s.State, s.Message)
			}
			return nil
		}
	}
}
