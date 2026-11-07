// Package tdx 实现通达信 7709 行情协议，负责连接、握手、报文重试和二进制解码。
package tdx

import (
	"bytes"
	"compress/zlib"
	"context"
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"time"

	"golang.org/x/text/encoding/simplifiedchinese"

	"quotes/internal/domain"
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

// 复用 core 中的领域类型，保持协议层接口简洁，也避免领域模型反向依赖网络实现。
type Host = domain.Host
type Security = domain.Security
type Bar = domain.Bar
type MinutePoint = domain.MinutePoint
type Transaction = domain.Transaction

// Client 由单个 worker 独占，不支持并发请求；失败后关闭连接再按线路轮换重连。
type Client struct {
	hosts   []Host          // 已通过业务自检的线路列表，connect 按 offset 开始逐条尝试。
	timeout time.Duration   // 每次拨号/握手/请求读写的期限，不是整次历史下载的总时限。
	conn    net.Conn        // 当前独占 TCP 连接，nil 时下次请求重连；失败必须关闭并清空。
	offset  int             // 下一次重连优先尝试的线路偏移，通过取模允许跨列表末端轮换。
	host    Host            // 最近一次成功拨号所用端点；不代表列表中所有端点均可用。
	ctx     context.Context // 任务取消上下文；拨号直接继承，阻塞读写由取消回调关闭连接打断。
	dial    DialFunc        // 可注入的统一出站拨号函数；nil 使用系统直连，见 SetDialer。
}

// DialFunc 与 net.Dialer.DialContext 同签名；组装根注入统一代理拨号器时使用。
type DialFunc func(ctx context.Context, network, address string) (net.Conn, error)

func NewClient(hosts []Host, timeout time.Duration, offset int) *Client {
	if timeout <= 0 {
		timeout = 8 * time.Second
	}
	return &Client{hosts: hosts, timeout: timeout, offset: offset, ctx: context.Background()}
}

// SetContext 将任务取消传播到拨号、握手和正在进行的请求；首次使用前设置。
func (c *Client) SetContext(ctx context.Context) { c.ctx = ctx }

// SetDialer 注入统一出站拨号函数（例如经代理隧道连接）；拨号期限仍按 timeout 约束。
// 只影响之后建立的新连接；不改变协议读写和线路轮换逻辑。
func (c *Client) SetDialer(dial DialFunc) { c.dial = dial }

func (c *Client) Close() {
	if c.conn != nil {
		_ = c.conn.Close()
		c.conn = nil
	}
}

func (c *Client) connect() error {
	// 流程：检查候选线路 → 从 offset 轮换拨号 → 设置期限/取消回调 → 完成握手。
	// 失败关闭本条连接并尝试下一条；只有握手成功才移动下一次重连的优先偏移。
	if len(c.hosts) == 0 {
		return fmt.Errorf("没有可用行情线路")
	}
	var lastErr error
	for i := 0; i < len(c.hosts); i++ {
		if err := c.ctx.Err(); err != nil {
			return err
		}
		// 各 worker 从不同偏移开始；成功后移动偏移，让下次重连优先尝试下一条线路。
		idx := (c.offset + i) % len(c.hosts)
		host := c.hosts[idx]
		// 拨号期限与原 net.Dialer 超时语义一致：注入统一拨号器时也按 timeout 约束。
		dialCtx, cancel := context.WithTimeout(c.ctx, c.timeout)
		var conn net.Conn
		var err error
		if c.dial != nil {
			conn, err = c.dial(dialCtx, "tcp", host.Address())
		} else {
			dialer := net.Dialer{Timeout: c.timeout}
			conn, err = dialer.DialContext(dialCtx, "tcp", host.Address())
		}
		cancel()
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
		// 握手期间关闭实际连接即可打断阻塞读写；回调捕获 conn，不访问可变的 c.conn。
		stopCancel := context.AfterFunc(c.ctx, func() { _ = conn.Close() })
		err = c.handshake()
		stopCancel()
		if err != nil {
			lastErr = err
			c.Close()
			continue
		}
		c.offset = idx + 1
		return nil
	}
	return fmt.Errorf("全部线路连接失败：%w", lastErr)
}

// handshake 前两步各读一次应答，第三步只发送初始化报文。
// 后续请求按业务命令号匹配应答，允许有限跳过服务器夹带的其他报文。
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

// requestSpec 描述一类业务报文的头字段和参数，request 据此组装完整帧。
// 字段沿用逆向协议名称，不是任务的 K 线周期；所有多字节整数均使用小端编码。
type requestSpec struct {
	payload  []byte // 命令参数正文，不含 commtype，也不含外层 10 字节请求头。
	category uint8  // 外层头第 1 字节的低位类型标记，不同业务使用不同值。
	version  uint16 // 请求头字节 3–4 的协议标记，按业务命令选择固定值。
	commtype uint16 // 业务命令号，放在正文前两字节；读取应答时用它匹配当前请求。
	hi       uint8  // 与 category 合成头部 16 位标记的高字节，0 时默认使用 0x08。
	flag5    uint8  // 请求头第 5 字节标记，通常默认 0x01；码表数量命令保留 0。
}

// request 统一组帧与三次尝试，所有长度/命令号均按协议的小端序编码。
// 正常连接可以复用，任何读写故障都废弃整条连接，不能在半帧位置继续读下一个请求。
func (c *Client) request(spec requestSpec) ([]byte, error) {
	// 第 1 步：填协议缺省值并组帧。请求布局（字节偏移从 0 开始）：
	// [0]0x0C | [1:3]类型标记 | [3:5]version | [5]flag | [6:8]/[8:10]两份正文长度。
	// 正文为 [10:12]commtype + payload；长度包含命令号，不包含外层 10 字节头。
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

	// 第 2 步：最多尝试 3 次；无连接时先轮换线路并握手，已有连接则继续串行使用。
	var lastErr error
	for attempt := 0; attempt < 3; attempt++ {
		if err := c.ctx.Err(); err != nil {
			c.Close()
			return nil, err
		}
		if c.conn == nil {
			if err := c.connect(); err != nil {
				lastErr = err
				continue
			}
		}
		// 第 3 步：刷新当前业务请求的读写期限，注册取消回调，再完整发送报文。
		_ = c.conn.SetDeadline(time.Now().Add(c.timeout))
		conn := c.conn
		stopCancel := context.AfterFunc(c.ctx, func() { _ = conn.Close() })
		if err := writeAll(c.conn, frame); err == nil {
			// 第 4 步：读取应答并匹配命令，最多跳过有限个无关报文；成功返回前移除取消回调。
			// 只接受当前命令的响应，并限制无关报文数量，避免异常服务端让循环永不结束。
			for i := 0; i < 8; i++ {
				response, commtype, readErr := readMessage(c.conn)
				if readErr != nil {
					lastErr = readErr
					break
				}
				if commtype == spec.commtype {
					stopCancel()
					return response, nil
				}
				lastErr = fmt.Errorf("收到无关应答 0x%04X", commtype)
			}
		} else {
			lastErr = err
		}
		// 第 5 步：本轮失败先移除回调，再关闭连接；下一轮一定重新握手，不尝试修补半帧。
		stopCancel()
		// 部分服务器在一次业务请求后就断开；统一关闭并重连，也能安全丢弃未读完的帧。
		c.Close()
	}
	return nil, fmt.Errorf("请求失败（已重试 3 次）：%w", lastErr)
}

// writeAll 处理短写：一次 Write 成功不代表整个报文已发出；零进展则按异常结束。
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

// readMessage 先读满固定头，再按压缩长度读满正文，TCP 分包不影响报文边界。
// 解压也设置上限并核对声明长度，避免损坏报文造成无界内存分配或被误当作合法行情。
func readMessage(r io.Reader) ([]byte, uint16, error) {
	// 响应头固定 16 字节；[10:12]命令号，[12:14]压缩正文长，[14:16]解压后正文长。
	// 处理顺序：读满头 → 校验长度 → 读满正文 → 长度不同时解压 → 核对解压长度。
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

// SecurityBars 只读取一页；跨页和日期筛选属于 download 用例层。
// 在转换为 uint16 前检查 start+count，防止历史位置截断后返回错误区间。
func (c *Client) SecurityBars(category int, market uint8, code string, start, count int) ([]Bar, error) {
	if len(code) != 6 || count < 1 || count > 800 || start < 0 || start > 65535 || count > 65536-start || category < 0 || category > 12 || market > 2 {
		return nil, fmt.Errorf("K 线参数无效：单次数量须为 1–800，历史位置不得超出 0–65535")
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
	if int(n) > count {
		c.Close()
		return nil, fmt.Errorf("行情应答数量 %d 超出请求数量 %d", n, count)
	}
	out := make([]Bar, 0, int(n))
	// 单页解码流程：条数 → 每根日期 → 四个价格差分 → 原始量额 → 更新上一根收盘基准。
	// base 只在本应答内累计，切换页面后从协议约定的零基准重新开始。
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
		// 价格使用差分编码：开盘相对上一根收盘，其余 OHLC 相对本根开盘；最后换算为元。
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

func parseHost(value string) (Host, error) { return domain.ParseHost(value) }
