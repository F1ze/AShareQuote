package service

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"quotes/internal/domain"
	"quotes/internal/repository"
)

type historicalFixture struct{ universeCalls, barsCalls int }

func (f *historicalFixture) Universe(context.Context, []int) ([]domain.Security, error) {
	f.universeCalls++
	return []domain.Security{
		{Market: 1, Code: "600000", Name: "active", ListingDate: "2020-01-01", Status: "listed"},
		{Market: 1, Code: "600001", Name: "delisted", ListingDate: "2019-01-01", DelistingDate: "2020-01-10", Status: "delisted"},
		{Market: 1, Code: "510300", Name: "ETF"},
	}, nil
}
func (f *historicalFixture) Bars(_ context.Context, _ domain.Security, _ int, from, to string) ([]domain.Bar, error) {
	f.barsCalls++
	day, _ := time.ParseInLocation("2006-01-02", from, exchangeTime)
	return []domain.Bar{{Date: day, Open: 10, High: 12, Low: 9, Close: 11, Volume: 1, Amount: 100}}, nil
}
func (f *historicalFixture) Calendar(context.Context, string, string) ([]domain.TradingDay, error) {
	return nil, nil
}

func awaitTask(t *testing.T, m *JobService) *TaskSnapshot {
	t.Helper()
	deadline := time.After(4 * time.Second)
	ticker := time.NewTicker(5 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-deadline:
			t.Fatal("task timeout")
			return nil
		case <-ticker.C:
			s := m.Snapshot()
			if s != nil && !activeState(s.State) {
				return s
			}
		}
	}
}

func TestHistoricalUniverseAndRestartResume(t *testing.T) {
	root := t.TempDir()
	output := filepath.Join(root, "archive")
	source := &historicalFixture{}
	m := newTestJobService(t, nil, output, nil)
	if err := configureTestStorage(m, filepath.Join(root, "jobs")); err != nil {
		t.Fatal(err)
	}
	_ = m.RegisterSource(SourceSpec{Key: "baostock", Label: "fixture", Adapter: source, Markets: []int{0, 1}, Categories: []int{9}})
	options := DownloadOptions{Source: "baostock", Storage: "csv", Markets: []int{1}, ASharesOnly: true, Category: 9, StartDate: "2019-01-01", EndDate: "2024-01-01"}
	if _, err := m.Start(options); err != nil {
		t.Fatal(err)
	}
	first := awaitTask(t, m)
	if first.State != "completed" || first.Succeeded != 2 || source.barsCalls != 2 {
		t.Fatalf("historical/delisted selection failed: %+v %+v", first, source)
	}
	restarted := newTestJobService(t, nil, output, nil)
	if err := configureTestStorage(restarted, filepath.Join(root, "jobs")); err != nil {
		t.Fatal(err)
	}
	_ = restarted.RegisterSource(SourceSpec{Key: "baostock", Label: "fixture", Adapter: source, Markets: []int{0, 1}, Categories: []int{9}})
	if _, err := restarted.Resume(first.ID); err != nil {
		t.Fatal(err)
	}
	second := awaitTask(t, restarted)
	if second.State != "completed" || second.Skipped != 2 || second.Bars != 0 || source.universeCalls != 1 || source.barsCalls != 2 {
		t.Fatalf("resume accessed live universe or duplicated reads: %+v %+v", second, source)
	}
	// A changed archive invalidates that checkpoint only. The saved security plan
	// remains stable; a new live stock list must not decide what gets recovered.
	if err := os.WriteFile(filepath.Join(output, "sh600000_day.csv"), []byte("date,open,high,low,close,volume,amount\n2020-01-01,10,12,9,12,1,100\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := restarted.Resume(first.ID); err != nil {
		t.Fatal(err)
	}
	third := awaitTask(t, restarted)
	if third.Skipped != 1 || source.universeCalls != 1 || source.barsCalls != 3 {
		t.Fatalf("invalidated checkpoint was not selectively refreshed: %+v %+v", third, source)
	}
}

type failOnceFixture struct {
	historicalFixture
	failed bool
}

func (f *failOnceFixture) Bars(ctx context.Context, security domain.Security, category int, from, to string) ([]domain.Bar, error) {
	if security.Code == "600000" && !f.failed {
		f.failed = true
		return nil, errors.New("temporary failure")
	}
	return f.historicalFixture.Bars(ctx, security, category, from, to)
}

func TestResumeRetriesOnlyFailedSecurities(t *testing.T) {
	root := t.TempDir()
	source := &failOnceFixture{}
	m := newTestJobService(t, nil, filepath.Join(root, "archive"), nil)
	if err := configureTestStorage(m, filepath.Join(root, "jobs")); err != nil {
		t.Fatal(err)
	}
	_ = m.RegisterSource(SourceSpec{Key: "baostock", Label: "fixture", Adapter: source, Markets: []int{0, 1}, Categories: []int{9}})
	options := DownloadOptions{Source: "baostock", Storage: "csv", Markets: []int{1}, ASharesOnly: true, Category: 9, StartDate: "2019-01-01", EndDate: "2024-01-01"}
	if _, err := m.Start(options); err != nil {
		t.Fatal(err)
	}
	first := awaitTask(t, m)
	if first.State != "completed_with_errors" || first.Succeeded != 1 || first.Failed != 1 {
		t.Fatalf("fixture did not produce one failed security: %+v", first)
	}
	if _, err := m.Resume(first.ID); err != nil {
		t.Fatal(err)
	}
	second := awaitTask(t, m)
	if second.State != "completed" || second.Succeeded != 2 || second.Failed != 0 || second.Skipped != 1 || second.Bars != 1 {
		t.Fatalf("resume did not retry only the failed security: %+v", second)
	}
	if source.universeCalls != 1 || source.barsCalls != 2 {
		t.Fatalf("resume changed the plan or downloaded a successful checkpoint: %+v", source)
	}
}

func TestDeleteJobKeepsMarketDataAndRejectsActiveRun(t *testing.T) {
	root := t.TempDir()
	j, err := repository.NewJobJournal(filepath.Join(root, "jobs"))
	if err != nil {
		t.Fatal(err)
	}
	id := "20260101-000000.000000001"
	s := TaskSnapshot{ID: id, State: "running", StartedAt: time.Now(), Options: DownloadOptions{Category: 9}}
	if err := j.Append(id, runEvent{Kind: "start", Snapshot: &s}); err != nil {
		t.Fatal(err)
	}
	s.State = "completed"
	if err := j.Append(id, runEvent{Kind: "finish", Snapshot: &s}); err != nil {
		t.Fatal(err)
	}
	m := newTestJobService(t, nil, root, nil)
	m.journal = j
	if err := m.Restore(); err != nil {
		t.Fatal(err)
	}
	archive := filepath.Join(root, "sh600000_day.csv")
	if err := os.WriteFile(archive, []byte("keep"), 0600); err != nil {
		t.Fatal(err)
	}
	m.current.mu.Lock()
	m.current.state = "running"
	m.current.mu.Unlock()
	if err := m.Delete(id); err == nil {
		t.Fatal("active task journal was deleted")
	}
	m.current.mu.Lock()
	m.current.state = "completed"
	m.current.mu.Unlock()
	if err := m.Delete(id); err != nil {
		t.Fatal(err)
	}
	if content, err := os.ReadFile(archive); err != nil || string(content) != "keep" {
		t.Fatalf("delete changed market archive: %q %v", content, err)
	}
	history, err := m.History()
	if err != nil || len(history) != 0 {
		t.Fatalf("deleted journal remains in history: %+v %v", history, err)
	}
	if err := m.Delete(id); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("missing journal error = %v", err)
	}
	if err := m.Delete("../invalid"); !errors.Is(err, domain.ErrInvalidInput) {
		t.Fatalf("invalid journal id error = %v", err)
	}
}

func TestTruncatedJournalAndInterruptedRecovery(t *testing.T) {
	dir := t.TempDir()
	j, err := repository.NewJobJournal(dir)
	if err != nil {
		t.Fatal(err)
	}
	id := "20260101-000000.000000001"
	s := TaskSnapshot{ID: id, State: "running", Total: 2, StartedAt: time.Now(), Options: DownloadOptions{Category: 9}}
	s.LineReport = &domain.LineReport{CheckedAt: time.Now().UTC(), Tested: 1, Available: 1, Results: []domain.HostStatus{{Name: "恢复测试站", Address: "127.0.0.1:7709", OK: true}}}
	if err := j.Append(id, runEvent{Kind: "start", Snapshot: &s}); err != nil {
		t.Fatal(err)
	}
	if err := j.Append(id, runEvent{Kind: "result", Completed: &checkpoint{File: FileResult{Name: "sh600000_day.csv", Bars: 1000}, Read: 7}}); err != nil {
		t.Fatal(err)
	}
	f, err := os.OpenFile(filepath.Join(dir, id+".jsonl"), os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = f.WriteString(`{"kind":"res`)
	f.Close()
	m := newTestJobService(t, nil, t.TempDir(), nil)
	if err := configureTestStorage(m, dir); err != nil {
		t.Fatal(err)
	}
	got := m.Snapshot()
	if got.State != "interrupted" || got.Done != 1 || got.Bars != 7 || got.EndedAt == nil {
		t.Fatalf("bad recovery: %+v", got)
	}
	if got.LineReport == nil || len(got.LineReport.Results) != 1 || got.LineReport.Results[0].Name != "恢复测试站" {
		t.Fatalf("重启后丢失历史检测报告：%+v", got.LineReport)
	}
}
