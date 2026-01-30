package util

import (
	"fmt"
	"math"
	"strconv"
	"time"
)

func FormatFileSize(size float64, human bool) string {
	if size <= 0 {
		return "0"
	}
	units := []string{"B", "KB", "MB", "GB", "TB", "PB", "EB"}
	group := 0
	if human {
		group = int(math.Log10(size) / math.Log10(1024))
		if group < 0 {
			group = 0
		}
		if group >= len(units) {
			group = len(units) - 1
		}
	}
	return fmt.Sprintf("%.2f %s", size/math.Pow(1024, float64(group)), units[group])
}

func ReadString(data []byte, count int, pos *int) string {
	if *pos+count > len(data) {
		count = len(data) - *pos
		if count < 0 {
			count = 0
		}
	}
	val := string(data[*pos : *pos+count])
	*pos += count
	return val
}

func ReadUint16(data []byte, pos *int) uint16 {
	if *pos+2 > len(data) {
		return 0
	}
	val := uint16(data[*pos])<<8 | uint16(data[*pos+1])
	*pos += 2
	return val
}

func ReadUint32(data []byte, pos *int) uint32 {
	if *pos+4 > len(data) {
		return 0
	}
	val := uint32(data[*pos])<<24 | uint32(data[*pos+1])<<16 | uint32(data[*pos+2])<<8 | uint32(data[*pos+3])
	*pos += 4
	return val
}

func ReadInt32(data []byte, pos *int) int32 {
	return int32(ReadUint32(data, pos))
}

func ReadByte(data []byte, pos *int) byte {
	if *pos >= len(data) {
		return 0
	}
	b := data[*pos]
	*pos += 1
	return b
}

func FormatTime(seconds float64, withMillis bool) string {
	d := time.Duration(seconds * float64(time.Second))
	h := int(d.Hours())
	m := int(d.Minutes()) % 60
	s := int(d.Seconds()) % 60
	ms := int(d.Milliseconds()) % 1000
	if withMillis {
		return fmt.Sprintf("%d:%02d:%02d.%03d", h, m, s, ms)
	}
	return fmt.Sprintf("%d:%02d:%02d", h, m, s)
}

// FormatNumber formats an integer with thousands separators.
func FormatNumber(n int64) string {
	if n == 0 {
		return "0"
	}
	sign := ""
	if n < 0 {
		sign = "-"
		n = -n
	}
	s := strconv.FormatInt(n, 10)
	if len(s) <= 3 {
		return sign + s
	}
	out := make([]byte, 0, len(s)+len(s)/3)
	for i, c := range s {
		if i != 0 && (len(s)-i)%3 == 0 {
			out = append(out, ',')
		}
		out = append(out, byte(c))
	}
	return sign + string(out)
}
