package repository

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"sync"

	"quotes/internal/domain"
)

// JobJournal 用独立 JSONL 文件保存每次执行，不和 DuckDB 共享事务。
type JobJournal struct {
	directory string     // 日志目录，文件名为 <执行编号>.jsonl，独立于行情输出目录。
	mu        sync.Mutex // 串行化整个单事件写入和日志读取，防止并发 worker 交错写半行。
}

var runIDPattern = regexp.MustCompile(`^[0-9]{8}-[0-9]{6}\.[0-9]{9}$`)

// append 用互斥锁保证多个 worker 的 JSON 行不交错，每条事件写完后同步到磁盘。
// 仅写入系统缓冲区还不算可靠断点，否则断电后界面成功数可能超前于可恢复记录。
func (j *JobJournal) Append(id string, event domain.JobEvent) error {
	j.mu.Lock()
	defer j.mu.Unlock()
	if !runIDPattern.MatchString(id) {
		return fmt.Errorf("任务编号无效")
	}
	data, err := json.Marshal(event)
	if err != nil {
		return err
	}
	f, err := os.OpenFile(filepath.Join(j.directory, id+".jsonl"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	defer f.Close()
	if _, err = f.Write(append(data, '\n')); err != nil {
		return err
	}
	return f.Sync()
}

// load 只重放日志，不验证资源文件，也不自动启动任务。
// 流程：校验编号 → 顺序读行 → 替换快照/计划并累加成功事件 → 检查至少有完整起始记录。
// 失败项没有单独的可恢复断点；它们的最终统计来自 finish 快照，恢复时仍会重新处理。
func (j *JobJournal) Load(id string) (domain.SavedRun, error) {
	j.mu.Lock()
	defer j.mu.Unlock()
	result := domain.SavedRun{Completed: make(map[string]domain.Checkpoint)}
	if !runIDPattern.MatchString(id) {
		return result, fmt.Errorf("任务编号无效")
	}
	f, err := os.Open(filepath.Join(j.directory, id+".jsonl"))
	if err != nil {
		return result, err
	}
	defer f.Close()
	// 逐行读取而非整体解码：允许崩溃只破坏最后一个未完成事件，之前完成的断点仍可使用。
	reader := bufio.NewReader(f)
	for {
		line, readErr := reader.ReadBytes('\n')
		if len(line) > 0 {
			var event domain.JobEvent
			if err := json.Unmarshal(line, &event); err != nil {
				// 崩溃可能只写了最后一行的一部分，允许舍弃它；中间坏行则不能静默跳过。
				if readErr == io.EOF {
					break
				}
				return result, fmt.Errorf("任务日志损坏：%w", err)
			}
			// 快照提供统计基线，之后的 result 事件逐条累加；finish 快照替换为最终状态。
			if event.Snapshot != nil {
				result.Snapshot = *event.Snapshot
			}
			if event.Plan != nil {
				result.Plan = event.Plan
			}
			if event.Completed != nil {
				c := *event.Completed
				result.Completed[c.File.Name] = c
				result.Snapshot.Done++
				result.Snapshot.Succeeded++
				result.Snapshot.Bars += int64(c.Read)
				if c.Skipped {
					result.Snapshot.Skipped++
				}
				if c.File.Warning != "" {
					result.Snapshot.Warnings++
				}
				result.Snapshot.Recent = append([]domain.FileResult{c.File}, result.Snapshot.Recent...)
				if len(result.Snapshot.Recent) > 10 {
					result.Snapshot.Recent = result.Snapshot.Recent[:10]
				}
			}
		}
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			return result, readErr
		}
	}
	if result.Snapshot.ID == "" {
		return result, fmt.Errorf("任务缺少完整起始记录")
	}
	return result, nil
}

// NewJobJournal 准备 JSONL 日志目录；服务只拿到接口，不负责创建目录或选择文件格式。
func NewJobJournal(directory string) (*JobJournal, error) {
	if err := os.MkdirAll(directory, 0700); err != nil {
		return nil, err
	}
	return &JobJournal{directory: directory}, nil
}

// Recent 按执行编号倒序读取有限份完整记录，不把中间日志损坏静默跳过。
func (j *JobJournal) Recent(limit int) ([]domain.SavedRun, error) {
	entries, err := os.ReadDir(j.directory)
	if err != nil {
		return nil, err
	}
	var ids []string
	for _, entry := range entries {
		if !entry.IsDir() && filepath.Ext(entry.Name()) == ".jsonl" {
			id := entry.Name()[:len(entry.Name())-6]
			if runIDPattern.MatchString(id) {
				ids = append(ids, id)
			}
		}
	}
	sort.Sort(sort.Reverse(sort.StringSlice(ids)))
	if len(ids) > limit {
		ids = ids[:limit]
	}
	out := make([]domain.SavedRun, 0, len(ids))
	for _, id := range ids {
		run, err := j.Load(id)
		if err != nil {
			return nil, err
		}
		out = append(out, run)
	}
	return out, nil
}
