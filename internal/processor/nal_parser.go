package processor

import (
	"bytes"
	"fmt"
)

// NALUnit NAL单元
type NALUnit struct {
	Start           []byte // 起始码
	Header          byte   // NAL头
	Data            []byte // NAL数据
	ForbiddenZeroBit byte
	NalRefIDC       byte
	NalUnitType     byte
}

// NAL起始码
var (
	NALStartFirst  = []byte{0x00, 0x00, 0x00, 0x01}
	NALStartSecond = []byte{0x00, 0x00, 0x01}
)

// ParseNALUnits 解析NAL单元
// 对齐cctvguid-nw dec.mjs:71-94 getNaluPos实现
func ParseNALUnits(data []byte) ([]*NALUnit, error) {
	positions := findNALPositions(data)
	if len(positions) < 2 {
		return nil, fmt.Errorf("未找到有效的NAL单元")
	}

	units := make([]*NALUnit, 0, len(positions)-1)
	for i := 0; i < len(positions)-1; i++ {
		start := positions[i]
		end := positions[i+1]

		if end-start < 5 { // 最小NAL单元：起始码(4) + 头(1)
			continue
		}

		unit, err := parseNALUnit(data[start:end])
		if err != nil {
			continue
		}
		units = append(units, unit)
	}

	return units, nil
}

// findNALPositions 搜索NAL起始码位置
func findNALPositions(data []byte) []int {
	var positions []int
	positions = append(positions, 0)

	for i := 2; i < len(data)-1; {
		if data[i] == 0x00 && data[i-1] == 0x00 {
			switch data[i+1] {
			case 0x00:
				// 0x00 0x00 0x00 0x01
				if i+2 < len(data) && data[i+2] == 0x01 {
					positions = append(positions, i-1)
					i += 3
					continue
				}
			case 0x01:
				// 0x00 0x00 0x01
				positions = append(positions, i-1)
				i += 2
				continue
			}
		}
		i++
	}

	positions = append(positions, len(data))
	return positions
}

// parseNALUnit 解析单个NAL单元
func parseNALUnit(data []byte) (*NALUnit, error) {
	if len(data) < 5 {
		return nil, fmt.Errorf("NAL数据太短")
	}

	// 确定起始码类型
	var start []byte
	var headerPos int

	if bytes.HasPrefix(data, NALStartFirst) {
		start = NALStartFirst
		headerPos = 4
	} else if bytes.HasPrefix(data, NALStartSecond) {
		start = NALStartSecond
		headerPos = 3
	} else {
		return nil, fmt.Errorf("无效的NAL起始码")
	}

	header := data[headerPos]
	unit := &NALUnit{
		Start:           start,
		Header:          header,
		Data:            data[headerPos+1:],
		ForbiddenZeroBit: (header >> 7) & 0x01,
		NalRefIDC:       (header >> 5) & 0x03,
		NalUnitType:     header & 0x1F,
	}

	return unit, nil
}

// Dump 输出NAL单元的字节表示
func (n *NALUnit) Dump() []byte {
	var buf bytes.Buffer
	buf.Write(n.Start)
	buf.WriteByte(n.Header)
	buf.Write(n.Data)
	return buf.Bytes()
}

// Reload 重新加载数据（解密后更新）
func (n *NALUnit) Reload(data []byte) {
	if len(data) > 0 {
		n.Header = data[0]
		if len(data) > 1 {
			n.Data = data[1:]
		}
		// 更新解析字段
		n.ForbiddenZeroBit = (n.Header >> 7) & 0x01
		n.NalRefIDC = (n.Header >> 5) & 0x03
		n.NalUnitType = n.Header & 0x1F
	}
}

// IsType25 是否为加密标记NAL
func (n *NALUnit) IsType25() bool {
	return n.NalUnitType == 25
}

// ShouldDecrypt 是否需要解密
func (n *NALUnit) ShouldDecrypt(shouldDecryptFlag bool) bool {
	return (n.NalUnitType == 1 || n.NalUnitType == 5) && shouldDecryptFlag
}
