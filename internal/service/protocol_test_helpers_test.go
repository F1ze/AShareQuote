package service

import (
	"encoding/binary"
	"io"

	"quotes/internal/domain"
)

const testBarsCommand = 0x052D

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

func writeTestMessage(w io.Writer, command uint16, body []byte) error {
	header := make([]byte, 16)
	binary.LittleEndian.PutUint16(header[10:12], command)
	binary.LittleEndian.PutUint16(header[12:14], uint16(len(body)))
	binary.LittleEndian.PutUint16(header[14:16], uint16(len(body)))
	if err := writeTestAll(w, header); err != nil {
		return err
	}
	return writeTestAll(w, body)
}

func writeTestAll(w io.Writer, data []byte) error {
	for len(data) > 0 {
		n, err := w.Write(data)
		if err != nil {
			return err
		}
		data = data[n:]
	}
	return nil
}

func readTestFrame(r io.Reader) ([]byte, []byte, error) {
	header := make([]byte, 10)
	if _, err := io.ReadFull(r, header); err != nil {
		return nil, nil, err
	}
	body := make([]byte, int(binary.LittleEndian.Uint16(header[6:8])))
	if _, err := io.ReadFull(r, body); err != nil {
		return nil, nil, err
	}
	return header, body, nil
}

func serveTestHandshake(conn io.ReadWriter) error {
	for range 2 {
		if _, _, err := readTestFrame(conn); err != nil {
			return err
		}
		if err := writeTestMessage(conn, 0, nil); err != nil {
			return err
		}
	}
	_, _, err := readTestFrame(conn)
	return err
}

func parseHost(value string) (domain.Host, error) { return domain.ParseHost(value) }
