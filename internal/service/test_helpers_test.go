package service

import (
	"context"
	"testing"

	"quotes/internal/domain"
	"quotes/internal/repository"
)

func newTestJobService(t *testing.T, provider LineProvider, output string, factory ClientFactory) *JobService {
	t.Helper()
	m, err := NewJobService(JobDependencies{DefaultOutput: output, Lines: provider, NewClient: factory, Archives: func(output string) Archive { return &repository.CSVArchive{Directory: output} }, Factors: repository.FileFactors{}})
	if err != nil {
		t.Fatal(err)
	}
	return m
}
func configureTestStorage(m *JobService, path string) error {
	j, err := repository.NewJobJournal(path)
	if err != nil {
		return err
	}
	m.journal = j
	return m.Restore()
}

// 旧回归场景中的直接写文件改为调用真实归档仓库；业务代码不保留文件写入后门。
func writeTestCSV(t *jobRun, security domain.Security, bars []domain.Bar) error {
	ctx := t.ctx
	if ctx == nil {
		ctx = context.Background()
	}
	_, err := (&repository.CSVArchive{Directory: t.output}).Commit(ctx, domain.Batch{Security: security, Category: t.options.Category, Bars: bars, Source: "fixture"})
	return err
}
