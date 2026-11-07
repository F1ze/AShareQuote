package sitecfg

import (
	"bytes"
	"os"
	"path/filepath"
	"strconv"
	"testing"
)

func TestSeedBuildMatchesPythonGolden(t *testing.T) {
	raw, result, err := Build(SeedSpec())
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Errors) != 0 || len(result.Warnings) != 12 {
		t.Fatalf("默认配置应仅有十二条主备跨段重复警告：%+v", result.Warnings)
	}
	golden, err := os.ReadFile(filepath.Join("testdata", "connect.cfg"))
	if err != nil {
		t.Fatal(err)
	}
	// Git 中的对照文本可保存为 LF；实际通达信配置必须为 CRLF。
	// 只归一化期望值，仍逐字节检查 Go 产物的内容和真实换行格式。
	golden = bytes.ReplaceAll(bytes.ReplaceAll(golden, []byte("\r\n"), []byte("\n")), []byte("\n"), []byte("\r\n"))
	if !bytes.Equal(raw, golden) {
		t.Fatalf("Go 生成配置与 Python 对照不一致：Go %d 字节，Python %d 字节", len(raw), len(golden))
	}
}

func TestBuildRejectsInvalidAddressWithoutSaving(t *testing.T) {
	spec := SeedSpec()
	spec.Hosts[0].IP = "not-an-address"
	path := filepath.Join(t.TempDir(), "connect.cfg")
	if _, err := Save(path, spec); err == nil {
		t.Fatal("非法地址必须阻断配置落盘")
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("自检失败时不应创建文件：%v", err)
	}
}

func TestParseRejectsUnknownDuplicateAndNonContinuousSections(t *testing.T) {
	raw, _, err := Build(SeedSpec())
	if err != nil {
		t.Fatal(err)
	}
	mutated := bytes.Replace(raw, []byte("HostNum=12"), []byte("HostNum=13"), 1)
	result, err := Parse(mutated)
	if err != nil || len(result.Errors) == 0 {
		t.Fatalf("应报告 HostNum 不一致：%v %v", result.Errors, err)
	}
	mutated = append(raw, []byte("[UNKNOWN]\r\nKey=Value\r\n[HQHOST]\r\n")...)
	result, err = Parse(mutated)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Errors) < 2 {
		t.Fatalf("应报告未知分区和重复分区错误：%v", result.Errors)
	}
}

// 参考文件的 TRYHQHOST 声明 9 条却实际列出 13 条；直接检查全部编号键，防止跟随旧 HostNum 漏站。
// 测试读取参考文件，生产 SeedSpec 仍是独立快照，不依赖 original 目录。
func TestSeedIncludesAllReferenceEndpoints(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "..", "..", "original", "connect.cfg"))
	if err != nil {
		t.Fatal(err)
	}
	reference, err := Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	_, built, err := Build(SeedSpec())
	if err != nil {
		t.Fatal(err)
	}
	for _, section := range hostSections {
		expected := reference.Sections[section]
		actual := built.Sections[section]
		indices := numberedIndices(expected)
		if actual["HostNum"] != strconv.Itoa(len(indices)) {
			t.Fatalf("[%s] 站点数量遗漏：%s，实际应有 %d", section, actual["HostNum"], len(indices))
		}
		for _, index := range indices {
			tag := strconv.Itoa(index)
			if index < 10 {
				tag = "0" + tag
			}
			for _, field := range []string{"HostName", "IPAddress", "Port"} {
				key := field + tag
				if actual[key] != expected[key] {
					t.Errorf("[%s] %s=%s，应为 %s", section, key, actual[key], expected[key])
				}
			}
		}
	}
}
