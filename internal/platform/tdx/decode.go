package tdx

import (
	"encoding/binary"
	"fmt"
	"math"
	"time"

	"quotes/internal/domain"
)

// decoder 仅在单次应答内使用；所有字段通过 take/u16/price 等方法共享同一游标。
type decoder struct {
	buf []byte // 当前报文已解压正文，不包含固定响应头。
	pos int    // 下一字段的字节偏移，take 成功后推进，失败时保持不变。
}

func (d *decoder) remaining() int { return len(d.buf) - d.pos }

// take 是所有定长字段读取的边界检查入口，先确认剩余长度再推进游标，避免切片越界。
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

// price 解析协议自定义的有符号变长整数，并非标准 varint。
// 首字节低 6 位是数值、0x40 是符号、0x80 表示续字节；后续每字节再提供 7 位数值。
func (d *decoder) price() (int64, error) {
	// 处理流程：首字节分离符号/低 6 位 → 逐个续字节拼 7 位 → 最后统一应用正负号。
	// 不能把符号当作补码扩展，也不能从一开始每字节都取 7 位，否则后续所有价格会错位。
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

// decodeVolume 解析通达信自定义的四字节量额编码，不能直接按 IEEE float32 解释。
// 各字节贡献不同的二进制指数分量，此处仅还原数值，成交量单位由上层数据约定决定。
func decodeVolume(raw uint32) float64 {
	// 第 1 步：按高到低拆四个字节；最高字节决定四个分量的指数，后面三个字节提供尾数分量。
	logpoint := int(raw >> 24)
	hheax := int((raw >> 24) & 0xFF)
	hleax := int((raw >> 16) & 0xFF)
	lheax := int((raw >> 8) & 0xFF)
	lleax := int(raw & 0xFF)
	// 第 2 步：按协议偏置计算四个指数。base 等价于 2^dwEcx，负指数通过倒数构造。
	dwEcx := logpoint*2 - 0x7F
	dwEdx := logpoint*2 - 0x86
	dwEsi := logpoint*2 - 0x8E
	dwEax := logpoint*2 - 0x96
	base := math.Pow(2, math.Abs(float64(dwEcx)))
	if dwEcx < 0 {
		base = 1 / base
	}
	// 第 3 步：次高字节的高位会改变其尾数拆分及后两分量的倍数，保留协议原分支处理。
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
	// 第 4 步：将各指数分量相加；这里不做“股/手”转换，也不能拿结果当 IEEE 位模式重解释。
	_ = hheax // 与 logpoint 同值，保留名称便于对照原始协议的四字节算法。
	return base + v4 + v3 + v1
}

// decodeDate 按周期选择日期布局：分钟线为压缩日期+当日分钟数，其他周期为 YYYYMMDD。
// 两种布局都生成市场时间，不能用宿主系统的本地时区解释。
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
		return time.Date(year, time.Month(month), day, int(minutes)/60, int(minutes)%60, 0, 0, domain.ExchangeTime), nil
	}
	ymd, err := d.u32()
	if err != nil {
		return time.Time{}, err
	}
	year := int(ymd / 10000)
	month := int((ymd % 10000) / 100)
	day := int(ymd % 100)
	return time.Date(year, time.Month(month), day, 0, 0, 0, 0, domain.ExchangeTime), nil
}
