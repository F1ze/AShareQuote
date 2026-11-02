package download

import (
	"encoding/csv"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

func TestParseCodes(t *testing.T) {
	codes, err := parseCodes("600000, sz000001, bj430047, 600000\n000002")
	if err != nil {
		t.Fatal(err)
	}
	got := make([]string, len(codes))
	for i, code := range codes {
		got[i] = marketPrefix(code.Market) + code.Code
	}
	want := []string{"sz000001", "sz000002", "sh600000", "bj430047"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("代码识别错误：got %v want %v", got, want)
	}
}

func TestWriteCSVReplacesCompleteFile(t *testing.T) {
	dir := t.TempDir()
	task := &DownloadTask{output: dir, options: DownloadOptions{Category: 9}}
	security := Security{Market: 1, Code: "600000"}
	bars := []Bar{{Date: time.Date(2024, 1, 2, 0, 0, 0, 0, time.Local), Open: 10, High: 12, Low: 9.5, Close: 11, Volume: 1234, Amount: 56789}}
	for range 2 {
		if err := task.writeCSV(security, bars); err != nil {
			t.Fatal(err)
		}
	}
	file, err := os.Open(filepath.Join(dir, "sh600000_day.csv"))
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	records, err := csv.NewReader(file).ReadAll()
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 2 || records[1][0] != "2024-01-02" || records[1][4] != "11.000" {
		t.Fatalf("CSV 内容错误：%v", records)
	}
	matches, err := filepath.Glob(filepath.Join(dir, "*.tmp"))
	if err != nil || len(matches) != 0 {
		t.Fatalf("临时文件未清理：%v %v", matches, err)
	}
}
