package service

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"quotes/internal/domain"
)

type recordingArchive struct {
	calls   *[]string
	failure error
}

func (a recordingArchive) Prepare() error { return nil }
func (a recordingArchive) Commit(context.Context, domain.Batch) (domain.Receipt, error) {
	*a.calls = append(*a.calls, "archive")
	return domain.Receipt{Path: "memory.csv", Rows: 1, From: "2024-01-02 00:00", To: "2024-01-02 00:00", SHA256: "digest", Bytes: 100}, a.failure
}
func (a recordingArchive) Latest(context.Context, domain.Security, int) (time.Time, error) {
	return time.Time{}, nil
}
func (a recordingArchive) State(context.Context, string) (domain.ArchiveState, error) {
	*a.calls = append(*a.calls, "inspect")
	return domain.ArchiveState{SHA256: "digest"}, nil
}
func (a recordingArchive) Verify(context.Context, domain.Checkpoint) (bool, error) { return true, nil }

type recordingWarehouse struct {
	calls   *[]string
	failure error
}

func (w recordingWarehouse) Ingest(context.Context, domain.Batch, domain.Receipt) error {
	*w.calls = append(*w.calls, "warehouse")
	return w.failure
}

type recordingJobs struct {
	calls   *[]string
	failure error
}

func (j recordingJobs) Append(_ string, event domain.JobEvent) error {
	if event.Kind != "result" {
		return errors.New("unexpected event")
	}
	*j.calls = append(*j.calls, "checkpoint")
	return j.failure
}
func (j recordingJobs) Load(string) (domain.SavedRun, error)  { return domain.SavedRun{}, nil }
func (j recordingJobs) Recent(int) ([]domain.SavedRun, error) { return nil, nil }
func (j recordingJobs) Delete(string) error                   { return nil }

// 成功断点是跨资源流水线的最后边界；不能因已经写出 CSV 就提前计成功。
func TestIngestionCheckpointsOnlyAfterRequiredResources(t *testing.T) {
	failure := errors.New("injected failure")
	for _, tc := range []struct {
		name                                 string
		archiveErr, warehouseErr, journalErr error
		want                                 []string
		success                              int
	}{
		{"archive failure", failure, nil, nil, []string{"archive"}, 0},
		{"warehouse failure", nil, failure, nil, []string{"archive", "warehouse"}, 0},
		{"journal failure", nil, nil, failure, []string{"archive", "warehouse", "inspect", "checkpoint"}, 0},
		{"success", nil, nil, nil, []string{"archive", "warehouse", "inspect", "checkpoint"}, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var calls []string
			run := &jobRun{ctx: context.Background(), started: time.Now(), archive: recordingArchive{&calls, tc.archiveErr}, warehouse: recordingWarehouse{&calls, tc.warehouseErr}, journal: recordingJobs{&calls, tc.journalErr}, output: "memory", options: DownloadOptions{Category: 9}}
			batch := domain.Batch{Security: domain.Security{Market: 1, Code: "600000"}, Category: 9, Bars: []domain.Bar{{Date: time.Date(2024, 1, 2, 0, 0, 0, 0, domain.ExchangeTime), Open: 10, High: 12, Low: 9, Close: 11}}}
			file, hash, err := run.persistBatch(batch, time.Now())
			run.recordOutcome(batch, file, hash, 1, err, false)
			if !reflect.DeepEqual(calls, tc.want) {
				t.Fatalf("资源边界次序：got %v want %v", calls, tc.want)
			}
			state := run.snapshot()
			if state.Succeeded != tc.success || state.Done != 1 || state.Failed != 1-tc.success {
				t.Fatalf("不能将部分提交报告为成功：%+v", state)
			}
		})
	}
}
