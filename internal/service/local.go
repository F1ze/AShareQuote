package service

import (
	"fmt"
	"sort"
	"time"

	"quotes/internal/domain"
)

// runLocal 先扫描并保存输入计划，再逐文件解码与保存；恢复时复用原计划，不重新定义范围。
// 单个文件失败只记录该项错误，其他文件继续处理，已经成功的归档可独立恢复。
func (t *jobRun) runLocal() {
	// 第 1 步：恢复用原输入计划，新任务扫描目录；只有拿到完整输入列表才设置总量。
	var inputs []domain.Input
	var err error
	if t.resumePlan != nil && len(t.resumePlan.Inputs) > 0 {
		inputs = t.resumePlan.Inputs
	} else {
		inputs, err = t.localSource.Discover(t.ctx, t.options.ImportDir)
	}
	if err != nil {
		t.finish("failed", err.Error())
		return
	}
	t.mu.Lock()
	t.total = len(inputs)
	t.state = "running"
	t.message = "正在校验并导入本地历史"
	t.mu.Unlock()
	// 第 2 步：计划先落盘，后处理内容，保证下次恢复时文件范围和每项周期仍保持不变。
	if t.journal != nil {
		s := t.snapshot()
		if err := t.journal.Append(t.id, runEvent{Kind: "planned", Snapshot: &s, Plan: &ingestionPlan{Inputs: inputs}}); err != nil {
			t.finish("failed", err.Error())
			return
		}
	}
	// 第 3 步：逐项复用断点或“解码→排序→归档→入库→记结果”，一个文件坏掉不影响其他文件。
	for _, input := range inputs {
		if t.ctx.Err() != nil {
			break
		}
		if t.restoreCheckpoint(input.Security, input.Category) {
			continue
		}
		started := time.Now()
		bars, err := t.localSource.Read(t.ctx, input, t.options.CSVVolumeUnit)
		sort.Slice(bars, func(i, j int) bool { return bars[i].Date.Before(bars[j].Date) })
		batch := domain.Batch{Security: input.Security, Category: input.Category, Bars: bars, Source: "local_history", Warning: "本地补录仅证明文件内有这些记录，尚未核验上市起点、交易日与停牌日覆盖", RunID: t.id}
		if len(bars) > 0 {
			batch.RequestedFrom = bars[0].Date.Format("2006-01-02")
			batch.RequestedTo = bars[len(bars)-1].Date.Format("2006-01-02")
		}
		var file FileResult
		var hash string
		if err == nil {
			file, hash, err = t.persistBatch(batch, started)
		}
		t.recordOutcome(batch, file, hash, len(bars), err, false)
	}
	// 第 4 步：取消优先；本地文件默认带覆盖未核验提示，即使全部导入也通常是带提示完成。
	s := t.snapshot()
	if t.ctx.Err() != nil {
		t.finish("cancelled", "补录已停止，完整归档已保留")
	} else if s.Succeeded == 0 {
		t.finish("failed", "没有成功导入的历史文件，请检查运行记录")
	} else if s.Failed > 0 || s.Warnings > 0 {
		t.finish("completed_with_errors", fmt.Sprintf("已导入 %d 个历史文件；请在数据仓库核对覆盖范围", s.Succeeded))
	} else {
		t.finish("completed", "历史补录完成")
	}
}
