package processlock

import (
	"path/filepath"
	"testing"
)

func TestSingleWriterAndRelease(t *testing.T) {
	path := filepath.Join(t.TempDir(), "qflux.lock")
	first, err := Acquire(path)
	if err != nil {
		t.Fatal(err)
	}
	if second, err := Acquire(path); err == nil {
		second.Close()
		first.Close()
		t.Fatal("second writer acquired lock")
	}
	first.Close()
	third, err := Acquire(path)
	if err != nil {
		t.Fatal("stale lock blocked restart", err)
	}
	third.Close()
}
