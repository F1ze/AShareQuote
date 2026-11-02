package tdx

import (
	"encoding/binary"
	"fmt"
	"math"
	"time"
)

type decoder struct {
	buf []byte
	pos int
}

func (d *decoder) remaining() int { return len(d.buf) - d.pos }

func (d *decoder) take(n int) ([]byte, error) {
	if n < 0 || d.remaining() < n {
		return nil, fmt.Errorf("应答数据不足：偏移 %d，需要 %d 字节，剩余 %d", d.pos, n, d.remaining())
	}
	out := d.buf[d.pos : d.pos+n]
	d.pos += n
	return out, nil
}

func (d *decoder) u8() (uint8, error) {
	b, err := d.take(1)
	if err != nil {
		return 0, err
	}
	return b[0], nil
}

func (d *decoder) u16() (uint16, error) {
	b, err := d.take(2)
	if err != nil {
		return 0, err
	}
	return binary.LittleEndian.Uint16(b), nil
}

func (d *decoder) i16() (int16, error) {
	n, err := d.u16()
	return int16(n), err
}

func (d *decoder) u32() (uint32, error) {
	b, err := d.take(4)
	if err != nil {
		return 0, err
	}
	return binary.LittleEndian.Uint32(b), nil
}

func (d *decoder) price() (int64, error) {
	n, err := d.u8()
	if err != nil {
		return 0, err
	}
	value := int64(n & 0x3F)
	negative := n&0x40 != 0
	shift := uint(6)
	for n&0x80 != 0 {
		n, err = d.u8()
		if err != nil {
			return 0, err
		}
		if shift >= 63 {
			return 0, fmt.Errorf("可变长整数溢出")
		}
		value += int64(n&0x7F) << shift
		shift += 7
	}
	if negative {
		value = -value
	}
	return value, nil
}

// decodeVolume decodes TongdaXin's 4-byte logarithmic floating-point value.
func decodeVolume(raw uint32) float64 {
	logpoint := int(raw >> 24)
	hheax := int((raw >> 24) & 0xFF)
	hleax := int((raw >> 16) & 0xFF)
	lheax := int((raw >> 8) & 0xFF)
	lleax := int(raw & 0xFF)
	dwEcx := logpoint*2 - 0x7F
	dwEdx := logpoint*2 - 0x86
	dwEsi := logpoint*2 - 0x8E
	dwEax := logpoint*2 - 0x96
	base := math.Pow(2, math.Abs(float64(dwEcx)))
	if dwEcx < 0 {
		base = 1 / base
	}
	var v4 float64
	if hleax > 0x80 {
		v4 = math.Pow(2, float64(dwEdx+1))*float64(hleax&0x7F) + math.Pow(2, float64(dwEdx))*128
	} else {
		v4 = math.Pow(2, float64(dwEdx)) * float64(hleax)
	}
	v3 := math.Pow(2, float64(dwEsi)) * float64(lheax)
	v1 := math.Pow(2, float64(dwEax)) * float64(lleax)
	if hleax&0x80 != 0 {
		v3 *= 2
		v1 *= 2
	}
	_ = hheax // retained to mirror the wire format's four component bytes.
	return base + v4 + v3 + v1
}

func decodeDate(category int, d *decoder) (time.Time, error) {
	if category < 4 || category == 7 || category == 8 {
		zipday, err := d.u16()
		if err != nil {
			return time.Time{}, err
		}
		minutes, err := d.u16()
		if err != nil {
			return time.Time{}, err
		}
		year := int(zipday>>11) + 2004
		month := int(zipday%2048) / 100
		day := int(zipday%2048) % 100
		return time.Date(year, time.Month(month), day, int(minutes)/60, int(minutes)%60, 0, 0, time.Local), nil
	}
	ymd, err := d.u32()
	if err != nil {
		return time.Time{}, err
	}
	year := int(ymd / 10000)
	month := int((ymd % 10000) / 100)
	day := int(ymd % 100)
	return time.Date(year, time.Month(month), day, 0, 0, 0, 0, time.Local), nil
}
