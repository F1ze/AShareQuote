package service

import (
	"context"
	"fmt"
	"strings"
	"time"

	"quotes/internal/domain"
)

// worker 是有界协程池中的一个执行者；整个任务同时运行最多 options.Workers 个 worker。
// 每个 worker 独占客户端并串行处理自己的证券，多个 worker 并行拉取不同证券。
// 当前客户端按顺序收发报文，未实现并发应答分发；共享同一 TCP 连接会混淆请求与应答。
func (t *jobRun) worker(offset int, jobs <-chan Security) {
	// worker 生命周期只创建一个独占客户端，逐证券流程如下：
	// 收任务 → 验证并复用旧断点 → 计算增量范围 → 取齐目标行情 → 保存资源 → 记录结果。
	client := t.newClient(t.hosts, time.Duration(t.options.Timeout)*time.Second, offset)
	if contextual, ok := client.(interface{ SetContext(context.Context) }); ok {
		contextual.SetContext(t.ctx)
	}
	defer client.Close()
	for {
		select {
		case <-t.ctx.Done():
			return
		case security, ok := <-jobs:
			if !ok || t.ctx.Err() != nil {
				return
			}
			// 1. 可复用项会直接重放仓库并记录结果，不再访问来源；无效项才重新获取。
			if t.restoreCheckpoint(security, t.options.Category) {
				continue
			}
			started := time.Now()
			options := t.options // 使用副本，单只证券的增量起点不能修改其他 worker 的配置。
			var err error
			// 2. 读取有效旧归档计算回看起点；旧归档损坏时传播错误，不按“没有历史”覆盖它。
			options, err = t.incrementalOptions(options, security)

			// 3. 拉取所有需要的页面；任何错误均跳过后面的资源写入，但仍记录该项失败。
			var bars []Bar
			var warning string
			if err == nil {
				bars, warning, err = t.fetchBarsWithOptions(client, security, options)
			}
			batch := domain.Batch{Security: security, Category: options.Category, Bars: bars, Source: "tdx_7709", RequestedFrom: options.StartDate, RequestedTo: options.EndDate, Warning: warning, RunID: t.id}
			var file FileResult
			var hash string
			// 4. 保存归档及可选仓库，再由 recordOutcome 最后写成功断点和界面进度。
			if err == nil {
				file, hash, err = t.persistBatch(batch, started)
			}
			t.recordOutcome(batch, file, hash, len(bars), err, false)
		}
	}
}

// persistBatch 按“原始归档 → 因子附件 → 数据仓库”的顺序保存单只证券。
// 文件和数据库不共享事务：入库失败仍保留已落盘的原始数据，重试时通过主键幂等合并。
// 成功断点由调用方最后写入，不能把只完成文件写入的证券标记为已完成。
func (t *jobRun) persistBatch(batch domain.Batch, started time.Time) (FileResult, string, error) {
	// 第 1 步：确认任务未取消，再合并原始 CSV，取得实际覆盖和内容哈希回执。
	var receipt domain.Receipt
	var err error
	if t.ctx.Err() != nil {
		return FileResult{}, "", t.ctx.Err()
	}
	receipt, err = t.archive.Commit(t.ctx, batch)
	if err != nil {
		return FileResult{}, "", err
	}
	// 第 2 步：仅当本次确实载入了 gbbq 时更新对应附件；未配置不等于清空既有因子。
	// 当前实现将 gbbq 写失败降级为覆盖警告；BaoStock 因子的失败策略在 remote.go 中处理。
	if t.factors != nil && domain.IsAShare(batch.Security) {
		events := t.factors[domain.FactorKey(batch.Security.Market, batch.Security.Code)]
		if err := t.factorRepository.SaveAdjustment(t.ctx, t.output, batch.Security, events); err != nil {
			batch.Warning += "；复权因子保存失败"
			t.log("warn", "复权因子保存失败：%v", err)
		}
	}
	// 第 3 步：可选仓库读取已提交的归档，在自身事务中完成行情及审计更新。
	// 入库失败不回滚 CSV，交给重试重放；这里返回错误，调用方不能记录成功断点。
	if t.warehouse != nil {
		if err := t.warehouse.Ingest(t.ctx, batch, receipt); err != nil {
			return FileResult{}, "", fmt.Errorf("原始归档已保留，入库失败可重试：%w", err)
		}
	}
	file := FileResult{Name: csvFileName(batch.Security, batch.Category), Code: batch.Security.Code, Market: strings.ToUpper(marketPrefix(batch.Security.Market)), Category: batch.Category, Bars: len(batch.Bars), Duration: time.Since(started).Milliseconds(), CreatedAt: time.Now().Format("15:04:05"), Warning: batch.Warning}
	// 第 4 步：构造展示结果；有归档回执时以整个合并文件覆盖为准，不只展示新批次的范围。
	file.From, file.To = batch.Bars[0].Date.Format("2006-01-02 15:04"), batch.Bars[len(batch.Bars)-1].Date.Format("2006-01-02 15:04")
	if t.archive != nil {
		file.From, file.To, file.Bars = receipt.From, receipt.To, receipt.Rows
	}
	file.Size = receipt.Bytes
	return file, receipt.SHA256, nil
}

// recordOutcome 先持久化成功断点，再更新界面计数；断点写入失败也按本次处理失败统计。
// read 表示本次读取量，file.Bars 表示合并后的文件总量，恢复时不能用后者冒充新增下载量。
func (t *jobRun) recordOutcome(batch domain.Batch, file FileResult, hash string, read int, err error, skipped bool) {
	// 第 1 步：取消后不再追加成功结果；即使资源已提交，恢复时也允许重新幂等处理。
	if t.ctx.Err() != nil {
		return
	}
	// 第 2 步：只有上游资源保存成功才计算附件哈希并落盘 result，落盘失败转入失败统计。
	if err == nil && t.journal != nil {
		c := checkpoint{File: file, SHA256: hash, Read: read, Skipped: skipped, Attachments: make(map[string]string)}
		state, stateErr := t.archive.State(t.ctx, file.Name)
		if stateErr != nil {
			err = stateErr
		} else if state.SHA256 != hash {
			err = fmt.Errorf("归档在记录断点前发生变化")
		} else {
			c.Attachments = state.Attachments
		}
		if err == nil {
			err = t.journal.Append(t.id, runEvent{Kind: "result", Completed: &c})
		}
	}
	// 第 3 步：短时间持锁更新 Done 与成功/失败计数；失败项不进入 Recent，也不累加 Bars。
	t.mu.Lock()
	t.done++
	if err != nil {
		t.failed++
		t.mu.Unlock()
		t.log("error", "%s%s 处理失败：%v", marketPrefix(batch.Security.Market), batch.Security.Code, err)
		return
	}
	t.success++
	t.bars += int64(read)
	if skipped {
		t.skipped++
	}
	if file.Warning != "" {
		t.warnings++
	}
	t.recent = append([]FileResult{file}, t.recent...)
	if len(t.recent) > 10 {
		t.recent = t.recent[:10]
	}
	t.mu.Unlock()
	if file.Warning != "" {
		t.log("warn", "%s%s：%s", marketPrefix(batch.Security.Market), batch.Security.Code, file.Warning)
	}
}

// restoreCheckpoint 返回 true 表示该证券已走恢复路径，调用方无需重新拉取来源。
// 即使重放入库失败也返回 true：错误已记入任务，不能再隐式下载一遍掩盖存储故障。
func (t *jobRun) restoreCheckpoint(security Security, category int) bool {
	// 1. 原任务没有此文件或资源哈希失效，返回 false，让正常采集路径重新处理。
	c, ok := t.resumeCompleted[csvFileName(security, category)]
	if !ok {
		return false
	}
	file, ok := t.resumeFile(t.ctx, security, category)
	if !ok {
		return false
	}
	// 2. 哈希有效也不能直接跳过仓库：数据库可能丢失或上次恰好在入库前退出，必须重放。
	// archive_resume 让仓库识别恢复路径，避免用旧计划回滚较新的人工参考资料。
	batch := domain.Batch{Security: security, Category: category, Source: "archive_resume", RequestedFrom: t.options.StartDate, RequestedTo: t.options.EndDate, Warning: file.Warning, RunID: t.id}
	var err error
	if t.warehouse != nil {
		receipt := domain.Receipt{Path: receiptPath(t.output, file.Name), Rows: file.Bars, From: file.From, To: file.To, SHA256: c.SHA256, VolumeUnit: "lots_100_shares"}
		err = t.warehouse.Ingest(t.ctx, batch, receipt)
	}
	// 3. 以零读取量记录本次结果；Skipped 只在重放成功时增加，失败会保留供用户再恢复。
	t.recordOutcome(batch, file, c.SHA256, 0, err, true)
	return true
}
