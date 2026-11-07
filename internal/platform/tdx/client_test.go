package tdx

import (
	"bytes"
	"compress/zlib"
	"context"
	"encoding/binary"
	"io"
	"net"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestSharedTrafficIncludesConnectionsHandshakeAndRequests(t *testing.T) {
	host := startTestServerN(t, 2, func(conn net.Conn) error {
		if err := serveTestHandshake(conn); err != nil {
			return err
		}
		if _, _, err := readTestFrame(conn); err != nil {
			return err
		}
		return writeTestMessage(conn, 0x044E, []byte{1, 0}, false)
	})
	traffic := NewTrafficController(1000, 50)
	started := time.Now()
	var wg sync.WaitGroup
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			client := NewClient([]Host{host}, time.Second, 0)
			defer client.Close()
			client.SetTrafficController(traffic)
			if count, err := client.SecurityCount(1); err != nil || count != 1 {
				t.Errorf("业务请求失败：%d %v", count, err)
			}
		}()
	}
	wg.Wait()
	// 两连接各消耗连接、三次握手、一次业务请求，共十个单站许可。
	if time.Since(started) < 175*time.Millisecond {
		t.Fatal("连接/握手或跨客户端共享限速被绕过")
	}
}

func TestSecurityBarsRejectsOffsetWrap(t *testing.T) {
	client := NewClient(nil, time.Second, 0)
	for _, params := range [][2]int{{65536, 1}, {-1, 1}, {65535, 2}, {0, 801}} {
		if _, err := client.SecurityBars(9, 1, "600000", params[0], params[1]); err == nil || !strings.Contains(err.Error(), "参数无效") {
			t.Fatalf("invalid request reached network: %v %v", params, err)
		}
	}
}

func TestCancelInterruptsInFlightRequest(t *testing.T) {
	peer, server := net.Pipe()
	defer server.Close()
	client := NewClient(nil, 30*time.Second, 0)
	client.conn = peer
	defer client.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	client.SetContext(ctx)
	ready := make(chan struct{})
	go func() { _, _, _ = readTestFrame(server); close(ready); _, _ = io.Copy(io.Discard, server) }()
	done := make(chan error, 1)
	go func() { _, err := client.SecurityBars(9, 1, "600000", 0, 10); done <- err }()
	select {
	case <-ready:
	case <-time.After(time.Second):
		t.Fatal("request did not start")
	}
	cancel()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("cancelled request succeeded")
		}
	case <-time.After(time.Second):
		t.Fatal("cancellation waited for network timeout")
	}
}

func appendTestPrice(dst []byte, value int64) []byte {
	negative := value < 0
	if negative {
		value = -value
	}
	first := byte(value & 0x3F)
	if negative {
		first |= 0x40
	}
	value >>= 6
	if value > 0 {
		first |= 0x80
	}
	dst = append(dst, first)
	for value > 0 {
		b := byte(value & 0x7F)
		value >>= 7
		if value > 0 {
			b |= 0x80
		}
		dst = append(dst, b)
	}
	return dst
}

func writeTestMessage(w io.Writer, commtype uint16, body []byte, compress bool) error {
	payload := body
	unzipSize := len(body)
	if compress {
		var compressed bytes.Buffer
		zw := zlib.NewWriter(&compressed)
		if _, err := zw.Write(body); err != nil {
			return err
		}
		if err := zw.Close(); err != nil {
			return err
		}
		payload = compressed.Bytes()
	}
	header := make([]byte, 16)
	binary.LittleEndian.PutUint16(header[10:12], commtype)
	binary.LittleEndian.PutUint16(header[12:14], uint16(len(payload)))
	binary.LittleEndian.PutUint16(header[14:16], uint16(unzipSize))
	if err := writeAll(w, header); err != nil {
		return err
	}
	return writeAll(w, payload)
}

func readTestFrame(r io.Reader) ([]byte, []byte, error) {
	header := make([]byte, 10)
	if _, err := io.ReadFull(r, header); err != nil {
		return nil, nil, err
	}
	length := int(binary.LittleEndian.Uint16(header[6:8]))
	body := make([]byte, length)
	if _, err := io.ReadFull(r, body); err != nil {
		return nil, nil, err
	}
	return header, body, nil
}

func startTestServer(t *testing.T, handler func(net.Conn) error) Host {
	t.Helper()
	return startTestServerN(t, 1, handler)
}

func startTestServerN(t *testing.T, count int, handler func(net.Conn) error) Host {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = listener.Close() })
	go func() {
		for range count {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			go func() {
				defer conn.Close()
				_ = handler(conn)
			}()
		}
	}()
	host, err := parseHost(listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	return host
}

func serveTestHandshake(conn net.Conn) error {
	for range 2 {
		if _, _, err := readTestFrame(conn); err != nil {
			return err
		}
		if err := writeTestMessage(conn, 0, nil, false); err != nil {
			return err
		}
	}
	_, _, err := readTestFrame(conn)
	return err
}

func TestReadMessageCompressed(t *testing.T) {
	var wire bytes.Buffer
	body := bytes.Repeat([]byte("tdx-bars,"), 100)
	if err := writeTestMessage(&wire, ctBars, body, true); err != nil {
		t.Fatal(err)
	}
	decoded, commtype, err := readMessage(&wire)
	if err != nil {
		t.Fatal(err)
	}
	if commtype != ctBars || !bytes.Equal(decoded, body) {
		t.Fatalf("解压应答不一致：comm=%#x len=%d", commtype, len(decoded))
	}
}

func TestSecurityCountUsesZeroFlag(t *testing.T) {
	flagSeen := make(chan byte, 1)
	host := startTestServer(t, func(conn net.Conn) error {
		if err := serveTestHandshake(conn); err != nil {
			return err
		}
		header, _, err := readTestFrame(conn)
		if err != nil {
			return err
		}
		flagSeen <- header[5]
		body := make([]byte, 2)
		binary.LittleEndian.PutUint16(body, 5432)
		return writeTestMessage(conn, 0x044E, body, false)
	})
	client := NewClient([]Host{host}, 2*time.Second, 0)
	defer client.Close()
	count, err := client.SecurityCount(1)
	if err != nil {
		t.Fatal(err)
	}
	if count != 5432 {
		t.Fatalf("证券数量错误：%d", count)
	}
	if flag := <-flagSeen; flag != 0 {
		t.Fatalf("证券数量请求 flag5 应为 0，实际为 %#x", flag)
	}
}

func TestSecurityBarsWireAndDecode(t *testing.T) {
	host := startTestServer(t, func(conn net.Conn) error {
		if err := serveTestHandshake(conn); err != nil {
			return err
		}
		header, body, err := readTestFrame(conn)
		if err != nil {
			return err
		}
		if len(body) != 28 { // commtype(u16) + 26-byte payload
			t.Errorf("K 线请求长度应为 28，实际 %d", len(body))
		}
		if header[5] != 1 || binary.LittleEndian.Uint16(body[:2]) != ctBars {
			t.Errorf("K 线请求头不匹配")
		}
		if got := binary.LittleEndian.Uint16(body[14:16]); got != 17 {
			t.Errorf("起始偏移应为 17，实际 %d", got)
		}
		if got := binary.LittleEndian.Uint16(body[16:18]); got != 100 {
			t.Errorf("条数应为 100，实际 %d", got)
		}

		response := make([]byte, 2)
		binary.LittleEndian.PutUint16(response, 1)
		date := make([]byte, 4)
		binary.LittleEndian.PutUint32(date, 20240102)
		response = append(response, date...)
		response = appendTestPrice(response, 10000) // open: base = 10.000
		response = appendTestPrice(response, 1000)  // close delta = +1.000
		response = appendTestPrice(response, 2000)  // high delta = +2.000
		response = appendTestPrice(response, -500)  // low delta = -0.500
		response = append(response, make([]byte, 8)...)
		return writeTestMessage(conn, ctBars, response, true)
	})
	client := NewClient([]Host{host}, 2*time.Second, 0)
	defer client.Close()
	bars, err := client.SecurityBars(9, 1, "600000", 17, 100)
	if err != nil {
		t.Fatal(err)
	}
	if len(bars) != 1 {
		t.Fatalf("K 线数量错误：%d", len(bars))
	}
	bar := bars[0]
	if bar.Open != 10 || bar.Close != 11 || bar.High != 12 || bar.Low != 9.5 {
		t.Fatalf("差分价格解码错误：%+v", bar)
	}
	if bar.Date.Format("2006-01-02") != "2024-01-02" {
		t.Fatalf("日期解码错误：%s", bar.Date)
	}
}

func TestDecoderRejectsTruncatedInput(t *testing.T) {
	d := decoder{buf: []byte{0x80}}
	if _, err := d.price(); err == nil {
		t.Fatal("截断的可变长整数应返回错误")
	}
}
