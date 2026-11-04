package filesys

import (
	"os"
	"path/filepath"
	"testing"
)

func TestBrowseAndCreateDirectory(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, "child"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "gbbq"), []byte("factor"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "notes.txt"), []byte("hidden"), 0o600); err != nil {
		t.Fatal(err)
	}
	browser := NewBrowser(root)
	folders, err := browser.Browse(root, "folder")
	if err != nil {
		t.Fatal(err)
	}
	if len(folders.Entries) != 1 || !folders.Entries[0].Directory || folders.Entries[0].Name != "child" {
		t.Fatalf("目录浏览结果错误：%+v", folders.Entries)
	}
	files, err := browser.Browse(root, "gbbq")
	if err != nil {
		t.Fatal(err)
	}
	if len(files.Entries) != 2 || files.Entries[1].Name != "gbbq" || files.Entries[1].Directory {
		t.Fatalf("权息文件浏览结果错误：%+v", files.Entries)
	}
	fallback, err := browser.Browse(filepath.Join(root, "missing", "nested"), "folder")
	if err != nil || fallback.Path != root {
		t.Fatalf("不存在的目录应回退到最近父目录：%+v, %v", fallback, err)
	}
	created, err := browser.CreateDirectory(root, "new-output")
	if err != nil {
		t.Fatal(err)
	}
	if info, err := os.Stat(created); err != nil || !info.IsDir() {
		t.Fatalf("新目录未创建：%v", err)
	}
	if _, err := browser.CreateDirectory(root, "../escape"); err == nil {
		t.Fatal("目录穿越名称应被拒绝")
	}
}
