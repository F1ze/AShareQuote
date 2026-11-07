package tdx

import (
	"archive/zip"
	"bytes"
	"encoding/binary"
	"fmt"
	"io"
	"strings"

	"golang.org/x/text/encoding/simplifiedchinese"
)

const (
	reportChunkSize = 0x7530
	maxReportSize   = 64 << 20
	maxBJConfigSize = 16 << 20
)

// BeijingSecurities 读取通达信北交所专用码表。公共行情站的 SecurityList(2)
// 不稳定或不应答，北交所代码和名称由 zhb.zip 内的 tdxbjmore.cfg 提供。
func (c *Client) BeijingSecurities() ([]Security, error) {
	raw, err := c.reportFile("zhb.zip")
	if err != nil {
		return nil, fmt.Errorf("下载北交所码表：%w", err)
	}
	zr, err := zip.NewReader(bytes.NewReader(raw), int64(len(raw)))
	if err != nil {
		return nil, fmt.Errorf("解压北交所码表：%w", err)
	}
	for _, file := range zr.File {
		if file.Name != "tdxbjmore.cfg" {
			continue
		}
		if file.UncompressedSize64 > maxBJConfigSize {
			return nil, fmt.Errorf("北交所码表文件过大：%d 字节", file.UncompressedSize64)
		}
		rc, err := file.Open()
		if err != nil {
			return nil, fmt.Errorf("打开北交所码表：%w", err)
		}
		data, readErr := io.ReadAll(io.LimitReader(rc, maxBJConfigSize+1))
		closeErr := rc.Close()
		if readErr != nil {
			return nil, fmt.Errorf("读取北交所码表：%w", readErr)
		}
		if closeErr != nil {
			return nil, fmt.Errorf("关闭北交所码表：%w", closeErr)
		}
		if len(data) > maxBJConfigSize {
			return nil, fmt.Errorf("北交所码表解压后过大")
		}
		securities, err := parseBeijingSecurities(data)
		if err != nil {
			return nil, err
		}
		if len(securities) == 0 {
			return nil, fmt.Errorf("北交所码表为空")
		}
		return securities, nil
	}
	return nil, fmt.Errorf("zhb.zip 中缺少 tdxbjmore.cfg")
}

// reportFile 按协议固定的 30000 字节窗口读取文件；短块或空块表示结束。
func (c *Client) reportFile(name string) ([]byte, error) {
	if name == "" || len(name) > 100 {
		return nil, fmt.Errorf("报表文件名无效")
	}
	var out []byte
	for offset := uint32(0); ; {
		payload := make([]byte, 108) // offset(4) + size(4) + filename(100)
		binary.LittleEndian.PutUint32(payload, offset)
		binary.LittleEndian.PutUint32(payload[4:], reportChunkSize)
		copy(payload[8:], name)
		body, err := c.request(requestSpec{payload: payload, category: 0x01, version: 0x0164, commtype: 0x06B9})
		if err != nil {
			return nil, err
		}
		if len(body) < 4 {
			return nil, fmt.Errorf("报表分块应答过短：%d 字节", len(body))
		}
		declared := binary.LittleEndian.Uint32(body[:4])
		if int(declared) != len(body)-4 {
			return nil, fmt.Errorf("报表分块长度不符：声明 %d，实际 %d", declared, len(body)-4)
		}
		chunk := body[4:]
		if len(chunk) == 0 {
			if len(out) == 0 {
				return nil, fmt.Errorf("报表 %s 无数据", name)
			}
			return out, nil
		}
		if len(chunk) > reportChunkSize || len(out)+len(chunk) > maxReportSize {
			return nil, fmt.Errorf("报表 %s 大小异常", name)
		}
		out = append(out, chunk...)
		offset += uint32(len(chunk))
		if len(chunk) < reportChunkSize {
			return out, nil
		}
	}
}

func parseBeijingSecurities(data []byte) ([]Security, error) {
	text, err := simplifiedchinese.GBK.NewDecoder().Bytes(data)
	if err != nil {
		return nil, fmt.Errorf("北交所码表 GBK 解码失败：%w", err)
	}
	var out []Security
	seen := make(map[string]bool)
	for lineNumber, line := range strings.Split(string(text), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		fields := strings.Split(line, "|")
		if len(fields) < 5 || fields[0] != "44" || fields[2] != "2" || !sixDigits(fields[1]) || strings.TrimSpace(fields[3]) == "" {
			return nil, fmt.Errorf("北交所码表第 %d 行格式非法", lineNumber+1)
		}
		if seen[fields[1]] {
			return nil, fmt.Errorf("北交所码表代码重复：%s", fields[1])
		}
		seen[fields[1]] = true
		out = append(out, Security{Market: 2, Code: fields[1], Name: strings.TrimSpace(fields[3])})
	}
	return out, nil
}

func sixDigits(value string) bool {
	if len(value) != 6 {
		return false
	}
	for _, r := range value {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}
