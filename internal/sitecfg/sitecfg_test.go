package sitecfg

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

func TestSeedBuildMatchesPythonGolden(t *testing.T) {
	raw, result, err := Build(SeedSpec())
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Errors) != 0 || len(result.Warnings) != 4 {
		t.Fatalf("默认配置应仅有四条跨段重复警告：%+v", result.Warnings)
	}
	golden, err := os.ReadFile(filepath.Join("testdata", "connect.cfg"))
	if err != nil {
		t.Fatal(err)
	}
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
	mutated := bytes.Replace(raw, []byte("HostNum=6"), []byte("HostNum=7"), 1)
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
