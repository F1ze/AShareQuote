// Package filesys provides constrained local path browsing for the web UI.
package filesys

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"quotes/internal/domain"
)

type Entry = domain.Entry
type Location = domain.Location
type BrowseResult = domain.BrowseResult

type Browser struct {
	output string // 默认行情输出路径，浏览请求为空或创建目录父路径为空时使用。
	home   string // 启动时用户主目录，gbbq 模式默认从这里查找，并作为快捷入口。
	work   string // 启动时工作目录，仅用于提供项目快捷入口，不限制浏览范围。
}

func NewBrowser(output string) *Browser {
	home, _ := os.UserHomeDir()
	work, _ := os.Getwd()
	return &Browser{output: output, home: home, work: work}
}

// Browse 仅列目录，或在 gbbq 模式额外列出同名文件，不执行文件内容或系统原生选择框。
// 流程：校验模式 → 解析/回退目录 → 枚举过滤 → 目录优先排序 → 截取 500 项并返回快捷入口。
// 这是本机路径选择功能，默认输出目录是起点而非访问沙箱，不能把 Roots 当成权限白名单。
func (b *Browser) Browse(path, kind string) (BrowseResult, error) {
	if kind != "folder" && kind != "gbbq" {
		return BrowseResult{}, fmt.Errorf("未知文件浏览类型 %q", kind)
	}
	if strings.TrimSpace(path) == "" {
		path = b.output
		if kind == "gbbq" && b.home != "" {
			path = b.home
		}
	}
	absolute, err := filepath.Abs(path)
	if err != nil {
		return BrowseResult{}, fmt.Errorf("路径无效：%w", err)
	}
	info, err := os.Stat(absolute)
	if err != nil {
		if !os.IsNotExist(err) {
			return BrowseResult{}, fmt.Errorf("无法读取目录：%w", err)
		}
		// 用户可能选择尚未创建的输出路径；逐级上移到最近的已存在祖先，而不是直接创建它。
		for os.IsNotExist(err) {
			parent := filepath.Dir(absolute)
			if parent == absolute {
				return BrowseResult{}, fmt.Errorf("无法读取目录：%w", err)
			}
			absolute = parent
			info, err = os.Stat(absolute)
		}
		if err != nil {
			return BrowseResult{}, fmt.Errorf("无法读取目录：%w", err)
		}
	}
	if !info.IsDir() {
		if kind == "gbbq" && strings.EqualFold(filepath.Base(absolute), "gbbq") {
			absolute = filepath.Dir(absolute)
		} else {
			return BrowseResult{}, fmt.Errorf("所选路径不是目录")
		}
	}
	items, err := os.ReadDir(absolute)
	if err != nil {
		return BrowseResult{}, fmt.Errorf("读取目录：%w", err)
	}
	result := BrowseResult{Path: absolute, Entries: make([]Entry, 0), Roots: b.locations()}
	parent := filepath.Dir(absolute)
	if parent != absolute {
		result.Parent = parent
	}
	for _, item := range items {
		directory := item.IsDir()
		if !directory && (kind == "folder" || !strings.EqualFold(item.Name(), "gbbq")) {
			continue
		}
		result.Entries = append(result.Entries, Entry{Name: item.Name(), Path: filepath.Join(absolute, item.Name()), Directory: directory})
	}
	sort.Slice(result.Entries, func(i, j int) bool {
		if result.Entries[i].Directory != result.Entries[j].Directory {
			return result.Entries[i].Directory
		}
		return strings.ToLower(result.Entries[i].Name) < strings.ToLower(result.Entries[j].Name)
	})
	if len(result.Entries) > 500 {
		result.Entries = result.Entries[:500]
	}
	return result, nil
}

func (b *Browser) CreateDirectory(path, name string) (string, error) {
	name = strings.TrimSpace(name)
	if name == "" || name == "." || name == ".." || strings.ContainsAny(name, `/\\`) {
		return "", fmt.Errorf("目录名称无效")
	}
	if strings.TrimSpace(path) == "" {
		path = b.output
	}
	absolute, err := filepath.Abs(path)
	if err != nil {
		return "", fmt.Errorf("父目录无效：%w", err)
	}
	info, err := os.Stat(absolute)
	if err != nil || !info.IsDir() {
		return "", fmt.Errorf("父目录不存在或不是目录")
	}
	target := filepath.Join(absolute, name)
	if err := os.MkdirAll(target, 0o755); err != nil {
		return "", fmt.Errorf("创建目录：%w", err)
	}
	return target, nil
}

func (b *Browser) locations() []Location {
	candidates := []Location{{Name: "默认输出", Path: b.output}, {Name: "用户目录", Path: b.home}, {Name: "项目目录", Path: b.work}}
	seen := make(map[string]bool)
	locations := make([]Location, 0, len(candidates))
	for _, item := range candidates {
		if item.Path == "" {
			continue
		}
		absolute, err := filepath.Abs(item.Path)
		if err != nil || seen[absolute] {
			continue
		}
		if info, err := os.Stat(absolute); err != nil || !info.IsDir() {
			continue
		}
		seen[absolute] = true
		item.Path = absolute
		locations = append(locations, item)
	}
	return locations
}
