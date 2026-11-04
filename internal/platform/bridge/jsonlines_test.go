package bridge

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func TestCancellationTerminatesAndRestartsWorker(t *testing.T) {
	python, err := exec.LookPath("python")
	if err != nil {
		t.Skip("Python not installed")
	}
	root := t.TempDir()
	script := filepath.Join(root, "worker.py")
	if err = os.WriteFile(script, []byte("import sys,time\nfor line in sys.stdin:\n time.sleep(30)\n"), 0600); err != nil {
		t.Fatal(err)
	}
	process := &Process{Python: python, Script: script}
	defer process.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancel()
	started := time.Now()
	if _, err = process.Call(ctx, map[string]string{"op": "wait"}); err == nil {
		t.Fatal("cancelled request succeeded")
	}
	if time.Since(started) > 3*time.Second {
		t.Fatal("worker was not interrupted")
	}
	if err = os.WriteFile(script, []byte("import sys,json\nfor line in sys.stdin:\n print(json.dumps({'data':{'ready':True}}),flush=True)\n"), 0600); err != nil {
		t.Fatal(err)
	}
	data, err := process.Call(context.Background(), map[string]string{"op": "ping"})
	if err != nil || string(data) != "{\"ready\": true}" {
		t.Fatalf("worker did not restart: %s %v", data, err)
	}
}
