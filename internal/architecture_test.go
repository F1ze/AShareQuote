package architecture_test

import (
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
)

// TestLayerDependencies 约束实际 import 方向，防止文档分层而代码再次跨层直连。
// 检查生产文件（包括其他操作系统的实现），测试可以自行组装真实适配器。
func TestLayerDependencies(t *testing.T) {
	_, filename, _, _ := runtime.Caller(0)
	root := filepath.Dir(filename)
	allowed := map[string][]string{
		"domain":     {},
		"service":    {"domain"},
		"repository": {"domain"},
		"handler":    {"domain", "service"},
	}
	for layer, dependencies := range allowed {
		err := filepath.WalkDir(filepath.Join(root, layer), func(path string, entry fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if entry.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
				return nil
			}
			file, err := parser.ParseFile(token.NewFileSet(), path, nil, parser.ImportsOnly)
			if err != nil {
				return err
			}
			for _, spec := range file.Imports {
				name, err := strconv.Unquote(spec.Path.Value)
				if err != nil {
					return err
				}
				if layer == "repository" && name == "os/exec" {
					t.Errorf("%s: Repository 必须直接访问存储，不能启动数据库工作进程", path)
				}
				if strings.HasPrefix(name, "quotes/internal/") {
					relative := strings.TrimPrefix(name, "quotes/internal/")
					ok := false
					for _, dependency := range dependencies {
						if relative == dependency {
							ok = true
						}
					}
					if !ok {
						t.Errorf("%s: %s 不得依赖 %s", path, layer, name)
					}
				}
				// 领域和服务不执行外部读写、不处理 HTTP，也不编解码传输协议。
				if layer == "domain" || layer == "service" {
					// net.JoinHostPort/SplitHostPort 是纯地址运算；真正网络访问与 HTTP 留在适配器。
					for _, forbidden := range []string{"os", "os/exec", "io", "io/fs", "net/http", "database/sql", "encoding/json"} {
						if name == forbidden {
							t.Errorf("%s: %s 不能导入外部 I/O/传输包 %s", path, layer, name)
						}
					}
					if strings.Contains(name, ".") && !strings.HasPrefix(name, "quotes/internal/") {
						t.Errorf("%s: 内层不能依赖外部框架 %s", path, name)
					}
				}
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
}
