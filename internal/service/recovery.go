package service

import (
	"context"
	"fmt"
	"time"

	"quotes/internal/domain"
)

// Restore 从已注入的任务仓库恢复展示状态，不启动任务，也不决定日志文件格式。
func (m *JobService) Restore() error {
	history, err := m.History()
	if err != nil {
		return err
	}
	if len(history) > 0 {
		s := history[0]
		// 上一次进程的运行协程已不存在，不能把磁盘中的 running 当作仍在执行。
		if activeState(s.State) || s.State == "interrupted" {
			s.State = "interrupted"
			s.Message = "程序曾中断，可从已验证的文件断点继续"
			now := time.Now()
			s.EndedAt = &now
		}
		m.current = &jobRun{id: s.ID, state: s.State, total: s.Total, done: s.Done, success: s.Succeeded, failed: s.Failed, bars: s.Bars, pages: s.Pages, warnings: s.Warnings, skipped: s.Skipped, message: s.Message, output: s.OutputDir, options: s.Options, started: s.StartedAt, ended: s.EndedAt, recent: s.Recent, logs: s.Logs}
	}
	return nil
}

// Shutdown 先发出取消信号，再有限等待任务收尾，确保资源关闭前有机会保存终态。
// 等待期限由入口控制，不能因外部数据源阻塞而无限拖延进程退出。
func (m *JobService) Shutdown(ctx context.Context) {
	task := m.currentRun()
	if task == nil {
		return
	}
	if activeState(task.snapshot().State) {
		_ = m.Cancel()
	}
	timer := time.NewTicker(20 * time.Millisecond)
	defer timer.Stop()
	for activeState(task.snapshot().State) {
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
		}
	}
}

// History 先按执行编号倒序选最近 50 份日志，再逐份重放。
// 当前仍活跃的任务使用实时内存快照；其他日志若停留在活动状态则显示 interrupted。
// 日志读取/结构损坏直接返回错误，不把异常日志悄悄伪装成“没有历史”。
func (m *JobService) History() ([]TaskSnapshot, error) {
	if m.journal == nil {
		return []TaskSnapshot{}, nil
	}
	runs, err := m.journal.Recent(50)
	if err != nil {
		return nil, err
	}
	out := make([]TaskSnapshot, 0, len(runs))
	for _, run := range runs {
		s := run.Snapshot
		if activeState(s.State) {
			current := m.Snapshot()
			if current != nil && current.ID == s.ID {
				s = *current
			} else {
				s.State = "interrupted"
			}
		}
		out = append(out, s)
	}
	return out, nil
}

// Resume 使用原选项和原计划创建一个新任务，原日志保留，便于追溯每次恢复过程。
func (m *JobService) Resume(id string) (TaskSnapshot, error) {
	if m.journal == nil {
		return TaskSnapshot{}, fmt.Errorf("未启用任务持久化")
	}
	run, err := m.journal.Load(id)
	if err != nil {
		return TaskSnapshot{}, err
	}
	return m.start(run.Snapshot.Options, &run)
}

// restoreEntirePlan 是所有资源均有效时的离线恢复捷径：直接重放入库，不探测行情线路。
// 只要有一项无法通过哈希验证，就交回正常流程逐项恢复或重新获取。
// 返回值表示“本函数是否接管本次任务”，不表示每个文件都已成功入库；结果必须看终态。
func (t *jobRun) restoreEntirePlan() bool {
	// 第 1 步：将网络证券计划统一转换为输入项；本地计划保留每个文件自己的周期。
	if t.resumePlan == nil {
		return false
	}
	inputs := t.resumePlan.Inputs
	if len(inputs) == 0 {
		for _, s := range t.resumePlan.Securities {
			inputs = append(inputs, domain.Input{Security: s, Category: t.options.Category})
		}
	}
	if len(inputs) == 0 {
		return false
	}
	// 第 2 步：先验证全计划；任何资源缺失/变更立即退回正常流程，避免错误宣称可以完全离线恢复。
	for _, input := range inputs {
		if _, ok := t.resumeFile(t.ctx, input.Security, input.Category); !ok {
			return false
		}
	}
	// 第 3 步：为新任务保存稳定 planned 事件，再逐项重放；重放时还会再次校验当前文件。
	t.mu.Lock()
	t.total = len(inputs)
	t.state = "running"
	t.message = "正在验证归档并重放入库断点"
	t.mu.Unlock()
	if t.journal != nil {
		s := t.snapshot()
		if err := t.journal.Append(t.id, runEvent{Kind: "planned", Snapshot: &s, Plan: t.resumePlan}); err != nil {
			t.finish("failed", err.Error())
			return true
		}
	}
	for _, input := range inputs {
		if t.ctx.Err() != nil {
			break
		}
		t.restoreCheckpoint(input.Security, input.Category)
	}
	// 第 4 步：聚合恢复结果并结束当前任务；入库失败和原有覆盖警告仍需对用户可见。
	s := t.snapshot()
	if t.ctx.Err() != nil {
		t.finish("cancelled", "断点恢复已停止")
	} else if s.Failed > 0 || s.Warnings > 0 {
		t.finish("completed_with_errors", "已重放可恢复断点；部分数据仍需检查覆盖或重试")
	} else {
		t.finish("completed", "已验证并恢复所有完成断点，无需重新访问行情源")
	}
	return true
}

// resumeFile 由归档仓库验证 CSV 和因子附件；服务只根据验证结果决定重取或复用。
func (t *jobRun) resumeFile(ctx context.Context, s Security, category int) (FileResult, bool) {
	c, ok := t.resumeCompleted[csvFileName(s, category)]
	if !ok || t.archive == nil {
		return FileResult{}, false
	}
	valid, err := t.archive.Verify(ctx, c)
	if err != nil || !valid {
		return FileResult{}, false
	}
	return c.File, true
}
