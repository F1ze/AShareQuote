// Package tdx implements the TongdaXin 7709 wire protocol.
package tdx

import (
	"bytes"
	"compress/zlib"
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"time"

	"golang.org/x/text/encoding/simplifiedchinese"

	"quotes/internal/core"
)

const (
	ctBars   = 0x052D
	ctMinute = 0x051D
	ctTick   = 0x0FC5

	catBars   = 0x01
	catMinute = 0x1B
	catTick   = 0x17

	verBars   = 0x0164
	verMinute = 0x0100
	verTick   = 0x0101

	maxMessageSize = 2 << 20
)

var (
	setup1 = []byte{0x0c, 0x02, 0x18, 0x93, 0x00, 0x01, 0x03, 0x00, 0x03, 0x00, 0x0d, 0x00, 0x01}
	setup2 = []byte{0x0c, 0x02, 0x18, 0x94, 0x00, 0x01, 0x03, 0x00, 0x03, 0x00, 0x0d, 0x00, 0x02}
	setup3 = []byte{0x0c, 0x03, 0x18, 0x99, 0x00, 0x01, 0x20, 0x00, 0x20, 0x00, 0x0f, 0xdb, 0xd5, 0xd0, 0xc9, 0xcc, 0xd6, 0xa4, 0xa8, 0xaf, 0x00, 0x00, 0x00, 0x8f, 0xc2, 0x25, 0x40, 0x13, 0x00, 0x00, 0xd5, 0x00, 0xc9, 0xcc, 0xbd, 0xf0, 0xd7, 0xea, 0x00, 0x00, 0x00, 0x02}
)

// Aliases keep the protocol API concise while the canonical domain models
// remain dependency-free in core.
type Host = core.Host
type Security = core.Security
type Bar = core.Bar
type MinutePoint = core.MinutePoint
type Transaction = core.Transaction

type Client struct {
	hosts   []Host
	timeout time.Duration
	conn    net.Conn
	offset  int
	host    Host
}

func NewClient(hosts []Host, timeout time.Duration, offset int) *Client {
	if timeout <= 0 {
		timeout = 8 * time.Second
	}
	return &Client{hosts: hosts, timeout: timeout, offset: offset}
}

func (c *Client) Close() {
	if c.conn != nil {
		_ = c.conn.Close()
		c.conn = nil
	}
}

func (c *Client) connect() error {
	if len(c.hosts) == 0 {
		return fmt.Errorf("没有可用行情线路")
	}
	var lastErr error
	for i := 0; i < len(c.hosts); i++ {
		idx := (c.offset + i) % len(c.hosts)
		host := c.hosts[idx]
		dialer := net.Dialer{Timeout: c.timeout}
		conn, err := dialer.Dial("tcp", host.Address())
		if err != nil {
			lastErr = err
			continue
		}
		if tcp, ok := conn.(*net.TCPConn); ok {
			_ = tcp.SetNoDelay(true)
		}
		_ = conn.SetDeadline(time.Now().Add(c.timeout))
		c.conn = conn
		c.host = host
		if err = c.handshake(); err != nil {
			lastErr = err
			c.Close()
			continue
		}
		c.offset = idx + 1
		return nil
	}
	return fmt.Errorf("全部线路连接失败：%w", lastErr)
}

func (c *Client) handshake() error {
	for _, packet := range [][]byte{setup1, setup2} {
		if err := writeAll(c.conn, packet); err != nil {
			return fmt.Errorf("握手写入失败：%w", err)
		}
		if _, _, err := readMessage(c.conn); err != nil {
			return fmt.Errorf("握手应答失败：%w", err)
		}
	}
	if err := writeAll(c.conn, setup3); err != nil {
		return fmt.Errorf("初始化写入失败：%w", err)
	}
	return nil
}

type requestSpec struct {
	payload  []byte
	category uint8
	version  uint16
	commtype uint16
	hi       uint8
	flag5    uint8
}

func (c *Client) request(spec requestSpec) ([]byte, error) {
	if spec.hi == 0 {
		spec.hi = 0x08
	}
	if spec.flag5 == 0 && spec.commtype != 0x044E {
		spec.flag5 = 0x01
	}
	body := make([]byte, 2+len(spec.payload))
	binary.LittleEndian.PutUint16(body, spec.commtype)
	copy(body[2:], spec.payload)
	frame := make([]byte, 10, 10+len(body))
	frame[0] = 0x0C
	binary.LittleEndian.PutUint16(frame[1:3], uint16(spec.hi)<<8|uint16(spec.category))
	binary.LittleEndian.PutUint16(frame[3:5], spec.version)
	frame[5] = spec.flag5
	binary.LittleEndian.PutUint16(frame[6:8], uint16(len(body)))
	binary.LittleEndian.PutUint16(frame[8:10], uint16(len(body)))
	frame = append(frame, body...)

	var lastErr error
	for attempt := 0; attempt < 3; attempt++ {
		if c.conn == nil {
			if err := c.connect(); err != nil {
				lastErr = err
				continue
			}
		}
		_ = c.conn.SetDeadline(time.Now().Add(c.timeout))
		if err := writeAll(c.conn, frame); err == nil {
			for i := 0; i < 8; i++ {
				response, commtype, readErr := readMessage(c.conn)
				if readErr != nil {
					lastErr = readErr
					break
				}
				if commtype == spec.commtype {
					return response, nil
				}
				lastErr = fmt.Errorf("收到无关应答 0x%04X", commtype)
			}
		} else {
			lastErr = err
		}
		// Some 7709 servers accept only one business request per connection.
		// Closing on any failure also discards partially read frames safely.
		c.Close()
	}
	return nil, fmt.Errorf("请求失败（已重试 3 次）：%w", lastErr)
}

func writeAll(w io.Writer, data []byte) error {
	for len(data) > 0 {
		n, err := w.Write(data)
		if err != nil {
			return err
		}
		if n == 0 {
			return io.ErrUnexpectedEOF
		}
		data = data[n:]
	}
	return nil
}

func readMessage(r io.Reader) ([]byte, uint16, error) {
	header := make([]byte, 16)
	if _, err := io.ReadFull(r, header); err != nil {
		return nil, 0, err
	}
	commtype := binary.LittleEndian.Uint16(header[10:12])
	zipSize := int(binary.LittleEndian.Uint16(header[12:14]))
	unzipSize := int(binary.LittleEndian.Uint16(header[14:16]))
	if zipSize > maxMessageSize || unzipSize > maxMessageSize {
		return nil, 0, fmt.Errorf("异常应答长度 zip=%d unzip=%d", zipSize, unzipSize)
	}
	raw := make([]byte, zipSize)
	if _, err := io.ReadFull(r, raw); err != nil {
		return nil, 0, err
	}
	if zipSize != unzipSize {
		zr, err := zlib.NewReader(bytes.NewReader(raw))
		if err != nil {
			return nil, 0, fmt.Errorf("zlib 初始化：%w", err)
		}
		defer zr.Close()
		raw, err = io.ReadAll(io.LimitReader(zr, maxMessageSize+1))
		if err != nil {
			return nil, 0, fmt.Errorf("zlib 解压：%w", err)
		}
		if len(raw) != unzipSize {
			return nil, 0, fmt.Errorf("解压长度不符：期望 %d，实际 %d", unzipSize, len(raw))
		}
	}
	return raw, commtype, nil
}

func (c *Client) SecurityCount(market uint8) (int, error) {
	payload := []byte{market, 0, 0x75, 0xC7, 0x33, 0x01}
	body, err := c.request(requestSpec{payload: payload, category: 0x0C, version: 0x016C, commtype: 0x044E, hi: 0x18, flag5: 0x00})
	if err != nil {
		return 0, err
	}
	d := decoder{buf: body}
	n, err := d.u16()
	return int(n), err
}

func (c *Client) SecurityList(market uint8, start, count int) ([]Security, error) {
	if count < 1 || count > 1000 {
		return nil, fmt.Errorf("码表单次数量须为 1–1000")
	}
	payload := make([]byte, 4)
	binary.LittleEndian.PutUint16(payload, uint16(market))
	binary.LittleEndian.PutUint16(payload[2:], uint16(start))
	body, err := c.request(requestSpec{payload: payload, category: 0x01, version: 0x0164, commtype: 0x0450, hi: 0x18})
	if err != nil {
		return nil, err
	}
	d := decoder{buf: body}
	n, err := d.u16()
	if err != nil {
		return nil, err
	}
	out := make([]Security, 0, int(n))
	for i := 0; i < int(n); i++ {
		record, err := d.take(29)
		if err != nil {
			return nil, err
		}
		nameRaw := bytes.TrimRight(record[8:16], "\x00")
		name, _ := simplifiedchinese.GBK.NewDecoder().Bytes(nameRaw)
		out = append(out, Security{Market: market, Code: string(record[:6]), Name: string(name)})
	}
	return out, nil
}

func (c *Client) SecurityBars(category int, market uint8, code string, start, count int) ([]Bar, error) {
	if len(code) != 6 || count < 1 || count > 800 || start < 0 {
		return nil, fmt.Errorf("K 线参数无效：代码须为 6 位，数量为 1–800")
	}
	payload := make([]byte, 26)
	binary.LittleEndian.PutUint16(payload, uint16(market))
	copy(payload[2:8], code)
	binary.LittleEndian.PutUint16(payload[8:10], uint16(category))
	binary.LittleEndian.PutUint16(payload[10:12], 1)
	binary.LittleEndian.PutUint16(payload[12:14], uint16(start))
	binary.LittleEndian.PutUint16(payload[14:16], uint16(count))
	body, err := c.request(requestSpec{payload: payload, category: catBars, version: verBars, commtype: ctBars})
	if err != nil {
		return nil, err
	}
	d := decoder{buf: body}
	n, err := d.u16()
	if err != nil {
		return nil, err
	}
	out := make([]Bar, 0, int(n))
	var base int64
	for i := 0; i < int(n); i++ {
		date, err := decodeDate(category, &d)
		if err != nil {
			return nil, err
		}
		dOpen, err := d.price()
		if err != nil {
			return nil, err
		}
		dClose, err := d.price()
		if err != nil {
			return nil, err
		}
		dHigh, err := d.price()
		if err != nil {
			return nil, err
		}
		dLow, err := d.price()
		if err != nil {
			return nil, err
		}
		volumeRaw, err := d.u32()
		if err != nil {
			return nil, err
		}
		amountRaw, err := d.u32()
		if err != nil {
			return nil, err
		}
		open := base + dOpen
		closePrice := open + dClose
		high := open + dHigh
		low := open + dLow
		base = closePrice
		out = append(out, Bar{Date: date, Open: float64(open) / 1000, Close: float64(closePrice) / 1000, High: float64(high) / 1000, Low: float64(low) / 1000, Volume: decodeVolume(volumeRaw), Amount: decodeVolume(amountRaw)})
	}
	return out, nil
}

func (c *Client) MinuteTimeData(market uint8, code string) ([]MinutePoint, error) {
	payload := make([]byte, 12)
	binary.LittleEndian.PutUint16(payload, uint16(market))
	copy(payload[2:8], code)
	body, err := c.request(requestSpec{payload: payload, category: catMinute, version: verMinute, commtype: ctMinute})
	if err != nil {
		return nil, err
	}
	if len(body) < 13 {
		return nil, fmt.Errorf("分时应答过短：%d 字节", len(body))
	}
	d := decoder{buf: body}
	n, err := d.u16()
	if err != nil {
		return nil, err
	}
	d.pos = 13
	out := make([]MinutePoint, 0, int(n))
	var last int64
	for i := 0; i < int(n); i++ {
		diff, err := d.price()
		if err != nil {
			return nil, err
		}
		volume, err := d.price()
		if err != nil {
			return nil, err
		}
		last += diff
		out = append(out, MinutePoint{Price: float64(last) / 100, Volume: volume})
	}
	return out, nil
}

func (c *Client) TransactionData(market uint8, code string, start, count int) ([]Transaction, error) {
	if len(code) != 6 || count < 1 || count > 2000 || start < 0 {
		return nil, fmt.Errorf("逐笔参数无效")
	}
	payload := make([]byte, 12)
	binary.LittleEndian.PutUint16(payload, uint16(market))
	copy(payload[2:8], code)
	binary.LittleEndian.PutUint16(payload[8:10], uint16(start))
	binary.LittleEndian.PutUint16(payload[10:12], uint16(count))
	body, err := c.request(requestSpec{payload: payload, category: catTick, version: verTick, commtype: ctTick})
	if err != nil {
		return nil, err
	}
	d := decoder{buf: body}
	n, err := d.u16()
	if err != nil {
		return nil, err
	}
	out := make([]Transaction, 0, int(n))
	var last int64
	for i := 0; i < int(n); i++ {
		minutes, err := d.u16()
		if err != nil {
			return nil, err
		}
		diff, err := d.price()
		if err != nil {
			return nil, err
		}
		volume, err := d.price()
		if err != nil {
			return nil, err
		}
		number, err := d.price()
		if err != nil {
			return nil, err
		}
		buyOrSell, err := d.price()
		if err != nil {
			return nil, err
		}
		if _, err = d.price(); err != nil {
			return nil, err
		}
		last += diff
		out = append(out, Transaction{Time: fmt.Sprintf("%02d:%02d", minutes/60, minutes%60), Price: float64(last) / 100, Volume: volume, Number: number, BuyOrSell: buyOrSell})
	}
	return out, nil
}

func parseHost(value string) (Host, error) { return core.ParseHost(value) }
