package tdx

// 协议来源：original/tdx_realtime.py 的 Worker._call_once、SNAP_FIELDS、parse_order。
// 保留其已验证的握手流水线与命令头，同时复用已有 readMessage/writeAll/decoder，
// 不启动 Python 旁路服务，不复制下载端的连接池，也不改变历史下载的报文。
import (
	"context"
	"encoding/binary"
	"fmt"
	"math/rand/v2"
	"time"

	"quotes/internal/domain"
)

// realtimeRequest 每次调用创建一个独占短连接；服务端可能在业务应答后主动断开。
// 流程：限流 → 统一拨号器 → 三条握手与业务帧连续写 → 按命令认领应答 → 关闭。
// 尝试最多三条不同线路；取消直接关闭实际连接，不能等到超时才释放请求。
func (c *Client) realtimeRequest(command uint16, payload []byte, snapshot bool) ([]byte, error) {
	if c.dial == nil || len(c.hosts) == 0 {
		return nil, fmt.Errorf("实时行情未配置统一拨号器或站点")
	}
	frame := make([]byte, 12+len(payload))
	frame[0] = 0x0c
	cat, version, flag, extra := uint16(0x0801), uint16(0x0300), byte(1), 2
	if snapshot {
		cat, version, flag, extra = 0x0218, 0x0093, 0, 0
	}
	binary.LittleEndian.PutUint16(frame[1:3], cat)
	binary.LittleEndian.PutUint16(frame[3:5], version)
	frame[5] = flag
	// 参考服务的声明长度约定：快照为 payload 长，成交族额外声明两字节命令长度。
	binary.LittleEndian.PutUint16(frame[6:8], uint16(len(payload)+extra))
	binary.LittleEndian.PutUint16(frame[8:10], uint16(len(payload)+extra))
	binary.LittleEndian.PutUint16(frame[10:12], command)
	copy(frame[12:], payload)
	packet := append(append(append(append([]byte{}, setup1...), setup2...), setup3...), frame...)
	var last error
	for attempt := 0; attempt < min(3, len(c.hosts)); attempt++ {
		if attempt > 0 {
			timer := time.NewTimer(time.Duration(100+rand.IntN(100)) * time.Millisecond)
			select {
			case <-c.ctx.Done():
				timer.Stop()
				return nil, c.ctx.Err()
			case <-timer.C:
			}
		}
		if err := c.waitTraffic(); err != nil {
			return nil, err
		}
		host := c.hosts[(c.offset+attempt)%len(c.hosts)]
		ctx, cancel := context.WithTimeout(c.ctx, c.timeout)
		conn, err := c.dial(ctx, "tcp", host.Address())
		if err != nil {
			cancel()
			last = err
			continue
		}
		deadline, _ := ctx.Deadline()
		_ = conn.SetDeadline(deadline)
		stop := context.AfterFunc(ctx, func() { _ = conn.Close() })
		var body []byte
		err = writeAll(conn, packet)
		if err == nil {
			for i := 0; i < 8; i++ {
				var got uint16
				body, got, err = readMessage(conn)
				if err != nil {
					break
				}
				if got == command {
					break
				}
				err = fmt.Errorf("无关实时应答 0x%04X", got)
			}
		}
		stop()
		_ = conn.Close()
		cancel()
		if err == nil {
			return body, nil
		}
		last = err
	}
	if err := c.ctx.Err(); err != nil {
		return nil, err
	}
	return nil, fmt.Errorf("实时行情读取失败：%w", last)
}

func (c *Client) RealtimeQuotes(stocks []Security) ([]domain.RealtimeQuote, error) {
	if len(stocks) == 0 || len(stocks) > 80 {
		return nil, fmt.Errorf("实时快照须为 1–80 只证券")
	}
	payload := make([]byte, 10, 12+7*len(stocks))
	payload[0] = 5
	binary.LittleEndian.PutUint16(payload[8:], uint16(len(stocks)))
	for _, s := range stocks {
		if !domain.IsTDXInstrument(s) {
			return nil, fmt.Errorf("无效行情证券代码 %s", s.Code)
		}
		payload = append(payload, s.Market)
		payload = append(payload, []byte(s.Code)...)
	}
	payload = append(payload, 0x0c, 0x01)
	body, err := c.realtimeRequest(0x053e, payload, true)
	if err != nil {
		return nil, err
	}
	return decodeRealtimeQuotes(body, stocks)
}

func decodeRealtimeQuotes(body []byte, stocks []Security) ([]domain.RealtimeQuote, error) {
	d := decoder{buf: body}
	if _, err := d.u16(); err != nil {
		return nil, err
	}
	n, err := d.u16()
	if err != nil {
		return nil, err
	}
	if int(n) > len(stocks) {
		return nil, fmt.Errorf("快照应答数量超出请求")
	}
	want := map[string]bool{}
	for _, s := range stocks {
		want[domain.MarketPrefix(s.Market)+s.Code] = true
	}
	seen := map[string]bool{}
	out := make([]domain.RealtimeQuote, 0, n)
	for i := 0; i < int(n); i++ {
		market, err := d.u8()
		if err != nil {
			return nil, err
		}
		code, err := d.take(6)
		if err != nil {
			return nil, err
		}
		q := domain.RealtimeQuote{Market: market, Code: string(code), Bids: make([]domain.BookLevel, 5), Asks: make([]domain.BookLevel, 5)}
		key := domain.MarketPrefix(market) + q.Code
		if market > 2 || !want[key] || seen[key] {
			return nil, fmt.Errorf("快照证券错配或重复：%s", key)
		}
		seen[key] = true
		if _, err = d.u16(); err != nil {
			return nil, err
		}
		base, err := d.price()
		if err != nil {
			return nil, err
		}
		// 0x053E 按证券品种缩放，包括昨收、OHLC 和五档。实时轮询无需重新扫描码表。
		scale := realtimePriceScale(Security{Market: market, Code: q.Code}, true)
		q.Price = float64(base) / scale
		for _, p := range []*float64{&q.LastClose, &q.Open, &q.High, &q.Low} {
			v, e := d.price()
			if e != nil {
				return nil, e
			}
			*p = float64(base+v) / scale
		}
		for j := 0; j < 4; j++ {
			v, e := d.price()
			if e != nil {
				return nil, e
			}
			if j == 2 {
				q.Volume = v
			}
		}
		raw, err := d.u32()
		if err != nil {
			return nil, err
		}
		if raw != 0 {
			q.Amount = decodeVolume(raw)
		}
		for j := 0; j < 4; j++ {
			if _, err = d.price(); err != nil {
				return nil, err
			}
		}
		for level := 0; level < 5; level++ {
			bid, e := d.price()
			if e != nil {
				return nil, e
			}
			ask, e := d.price()
			if e != nil {
				return nil, e
			}
			bv, e := d.price()
			if e != nil {
				return nil, e
			}
			av, e := d.price()
			if e != nil {
				return nil, e
			}
			q.Bids[level] = domain.BookLevel{Price: float64(base+bid) / scale, Volume: bv}
			q.Asks[level] = domain.BookLevel{Price: float64(base+ask) / scale, Volume: av}
		}
		if _, err = d.take(2); err != nil {
			return nil, err
		}
		for j := 0; j < 4; j++ {
			if _, err = d.price(); err != nil {
				return nil, err
			}
		}
		if _, err = d.take(4); err != nil {
			return nil, err
		}
		out = append(out, q)
	}
	if d.remaining() != 0 {
		return nil, fmt.Errorf("快照存在未解析字节：%d", d.remaining())
	}
	return out, nil
}

func (c *Client) RealtimeTrades(stock Security) ([]domain.RealtimeTrade, error) {
	if !domain.IsTDXInstrument(stock) {
		return nil, fmt.Errorf("无效行情证券代码")
	}
	payload := make([]byte, 12)
	binary.LittleEndian.PutUint16(payload, uint16(stock.Market))
	copy(payload[2:8], stock.Code)
	binary.LittleEndian.PutUint16(payload[10:], 200)
	body, err := c.realtimeRequest(0x0fc2, payload, false)
	if err != nil {
		return nil, err
	}
	return decodeRealtimeTrades(body, realtimePriceScale(stock, false))
}

// 0x0FC2 与快照不同：转债逐笔使用 3 位小数，快照使用 4 位。
func realtimePriceScale(stock Security, snapshot bool) float64 {
	if domain.IsConvertibleBond(stock) && snapshot {
		return 10000
	}
	if domain.IsConvertibleBond(stock) || domain.IsETF(stock) {
		return 1000
	}
	return 100
}

func decodeRealtimeTrades(body []byte, scale float64) ([]domain.RealtimeTrade, error) {
	d := decoder{buf: body}
	n, err := d.u16()
	if err != nil {
		return nil, err
	}
	if n > 200 {
		return nil, fmt.Errorf("成交窗口超过 200 条")
	}
	out := make([]domain.RealtimeTrade, 0, n)
	var base int64
	for i := 0; i < int(n); i++ {
		minute, e := d.u16()
		if e != nil {
			return nil, e
		}
		delta, e := d.price()
		if e != nil {
			return nil, e
		}
		volume, e := d.price()
		if e != nil {
			return nil, e
		}
		trades, e := d.price()
		if e != nil {
			return nil, e
		}
		side, e := d.u16()
		if e != nil {
			return nil, e
		}
		second, e := d.u8()
		if e != nil {
			return nil, e
		}
		if minute >= 1440 || second >= 60 || volume < 0 || trades < 0 {
			return nil, fmt.Errorf("成交明细字段无效")
		}
		base += delta
		out = append(out, domain.RealtimeTrade{Time: fmt.Sprintf("%02d:%02d:%02d", minute/60, minute%60, second), Price: float64(base) / scale, Volume: volume, Trades: trades, Side: side})
	}
	if d.remaining() != 0 {
		return nil, fmt.Errorf("成交明细存在未解析字节：%d", d.remaining())
	}
	return out, nil
}
