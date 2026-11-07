package tdx

import (
	"archive/zip"
	"bytes"
	"encoding/binary"
	"fmt"
	"net"
	"testing"
	"time"

	"golang.org/x/text/encoding/simplifiedchinese"
)

func TestBeijingSecuritiesDownloadsChunksAndDecodesGBK(t *testing.T) {
	config := beijingTestConfig(t, "44|920000|2|安徽凤凰|1|\r\n44|920001|2|纬达光电|1|\r\n")
	for i := range 4000 {
		config = fmt.Appendf(config, "# %08x%08x%08x\r\n", i*2654435761, i*2246822519, i*3266489917)
	}
	archive := beijingTestZip(t, "tdxbjmore.cfg", config)
	if len(archive) <= reportChunkSize {
		t.Fatal("测试 ZIP 未覆盖跨块下载")
	}
	requests := 0
	host := startTestServer(t, func(conn net.Conn) error {
		if err := serveTestHandshake(conn); err != nil {
			return err
		}
		for offset := 0; offset < len(archive); {
			header, body, err := readTestFrame(conn)
			if err != nil {
				return err
			}
			requests++
			if header[1] != 0x01 || header[2] != 0x08 || binary.LittleEndian.Uint16(header[3:5]) != 0x0164 || header[5] != 1 {
				return fmt.Errorf("文件请求帧头错误：%x", header[:6])
			}
			if len(body) != 110 || binary.LittleEndian.Uint16(body[:2]) != 0x06B9 {
				return fmt.Errorf("文件请求体错误：%d 字节", len(body))
			}
			start := int(binary.LittleEndian.Uint32(body[2:6]))
			size := int(binary.LittleEndian.Uint32(body[6:10]))
			if start != offset || size != reportChunkSize || string(body[10:17]) != "zhb.zip" {
				return fmt.Errorf("分块参数错误：start=%d size=%d", start, size)
			}
			end := min(start+size, len(archive))
			chunk := archive[start:end]
			response := make([]byte, 4, 4+len(chunk))
			binary.LittleEndian.PutUint32(response, uint32(len(chunk)))
			response = append(response, chunk...)
			if err := writeTestMessage(conn, 0x06B9, response, true); err != nil {
				return err
			}
			offset = end
		}
		return nil
	})
	client := NewClient([]Host{host}, 2*time.Second, 0)
	defer client.Close()
	securities, err := client.BeijingSecurities()
	if err != nil {
		t.Fatal(err)
	}
	if requests < 2 {
		t.Fatalf("未发生跨块请求：%d", requests)
	}
	if len(securities) != 2 || securities[0].Market != 2 || securities[0].Code != "920000" || securities[0].Name != "安徽凤凰" || securities[1].Name != "纬达光电" {
		t.Fatalf("北交所码表解析错误：%+v", securities)
	}
}

func TestParseBeijingSecuritiesRejectsInvalidRecords(t *testing.T) {
	for _, line := range []string{
		"44|92000|2|代码不足|1|",
		"44|920000|1|市场错误|1|",
		"43|920000|2|类型错误|1|",
		"44|920000|2||1|",
		"44|920000|2|重复|1|\n44|920000|2|重复|1|",
	} {
		t.Run(line, func(t *testing.T) {
			if _, err := parseBeijingSecurities([]byte(line)); err == nil {
				t.Fatal("非法码表记录被接受")
			}
		})
	}
}

func TestBeijingSecuritiesRejectsMissingOrEmptyConfig(t *testing.T) {
	for _, tc := range []struct {
		name, entry string
		data        []byte
	}{
		{name: "缺少文件", entry: "other.cfg", data: []byte("44|920000|2|安徽凤凰|1|")},
		{name: "空目录", entry: "tdxbjmore.cfg", data: nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			archive := beijingTestZip(t, tc.entry, tc.data)
			host := startTestServer(t, func(conn net.Conn) error {
				if err := serveTestHandshake(conn); err != nil {
					return err
				}
				_, _, err := readTestFrame(conn)
				if err != nil {
					return err
				}
				response := make([]byte, 4, 4+len(archive))
				binary.LittleEndian.PutUint32(response, uint32(len(archive)))
				response = append(response, archive...)
				return writeTestMessage(conn, 0x06B9, response, false)
			})
			client := NewClient([]Host{host}, 2*time.Second, 0)
			defer client.Close()
			if _, err := client.BeijingSecurities(); err == nil {
				t.Fatal("无效北交所码表被接受")
			}
		})
	}
}

func beijingTestConfig(t *testing.T, asciiWithChinese string) []byte {
	t.Helper()
	encoded, err := simplifiedchinese.GBK.NewEncoder().Bytes([]byte(asciiWithChinese))
	if err != nil {
		t.Fatal(err)
	}
	return encoded
}

func beijingTestZip(t *testing.T, name string, data []byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	file, err := zw.Create(name)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := file.Write(data); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}
