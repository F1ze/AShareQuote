package tdx

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"io"
	"net"
	"os"
	"quotes/internal/domain"
	"reflect"
	"testing"
	"time"
)

// Fixture 的期望值由 original/tdx_realtime.py 的原始解析器生成，测试不是拿 Go 实现自证。
func realtimeFixture(t *testing.T) ([]byte, []byte, []domain.RealtimeQuote, []domain.RealtimeTrade) {
	t.Helper()
	raw, err := os.ReadFile("testdata/realtime.json")
	if err != nil {
		t.Fatal(err)
	}
	var fixture struct {
		SnapshotHex string
		TradesHex   string
		Quotes      []domain.RealtimeQuote
		Trades      []domain.RealtimeTrade
	}
	if err = json.Unmarshal(raw, &fixture); err != nil {
		t.Fatal(err)
	}
	snapshot, err := hex.DecodeString(fixture.SnapshotHex)
	if err != nil {
		t.Fatal(err)
	}
	trades, err := hex.DecodeString(fixture.TradesHex)
	if err != nil {
		t.Fatal(err)
	}
	return snapshot, trades, fixture.Quotes, fixture.Trades
}

func TestRealtimeDecodersMatchOriginalPython(t *testing.T) {
	snapshot, trades, wantQuotes, wantTrades := realtimeFixture(t)
	stocks := []Security{{Market: 1, Code: "600036"}, {Market: 0, Code: "000001"}}
	quotes, err := decodeRealtimeQuotes(snapshot, stocks)
	if err != nil || !reflect.DeepEqual(quotes, wantQuotes) {
		t.Fatalf("快照与参考模块不同：%+v %v", quotes, err)
	}
	gotTrades, err := decodeRealtimeTrades(trades)
	if err != nil || !reflect.DeepEqual(gotTrades, wantTrades) {
		t.Fatalf("成交与参考模块不同：%+v %v", gotTrades, err)
	}
	for i := 0; i < len(snapshot); i++ {
		if _, err := decodeRealtimeQuotes(snapshot[:i], stocks); err == nil {
			t.Fatalf("接受截断快照 %d", i)
		}
	}
	for i := 0; i < len(trades); i++ {
		if _, err := decodeRealtimeTrades(trades[:i]); err == nil {
			t.Fatalf("接受截断成交 %d", i)
		}
	}
	copy(snapshot[5:11], "600839")
	if _, err := decodeRealtimeQuotes(snapshot, stocks); err == nil {
		t.Fatal("把错配证券强行回填为当前股票")
	}
}

func TestRealtimePipelinesReferenceHandshakeAndFrame(t *testing.T) {
	snapshot, _, _, _ := realtimeFixture(t)
	host := startTestServer(t, func(conn net.Conn) error {
		want := append(append(append([]byte{}, setup1...), setup2...), setup3...)
		got := make([]byte, len(want))
		if _, err := io.ReadFull(conn, got); err != nil {
			return err
		}
		if !bytes.Equal(got, want) {
			t.Error("握手与参考模块不同")
		}
		// 故意不给握手应答，验证业务包已连续发送，没有等待前两次握手 RTT。
		header := make([]byte, 12)
		if _, err := io.ReadFull(conn, header); err != nil {
			return err
		}
		if header[5] != 0 || binary.LittleEndian.Uint16(header[1:3]) != 0x0218 || binary.LittleEndian.Uint16(header[10:]) != 0x053e {
			t.Errorf("快照头错误：%x", header)
		}
		length := int(binary.LittleEndian.Uint16(header[6:8]))
		payload := make([]byte, length)
		if _, err := io.ReadFull(conn, payload); err != nil {
			return err
		}
		if length != 26 || !bytes.Equal(payload[len(payload)-2:], []byte{0x0c, 0x01}) || binary.LittleEndian.Uint16(payload[8:10]) != 2 {
			t.Errorf("证券表尾或数量错位：%x", payload)
		}
		return writeTestMessage(conn, 0x053e, snapshot, false)
	})
	client := NewClient([]Host{host}, time.Second, 0)
	client.SetDialer((&net.Dialer{}).DialContext)
	client.SetContext(context.Background())
	quotes, err := client.RealtimeQuotes([]Security{{Market: 1, Code: "600036"}, {Market: 0, Code: "000001"}})
	if err != nil || len(quotes) != 2 {
		t.Fatalf("流水线快照失败：%v %v", quotes, err)
	}
}
