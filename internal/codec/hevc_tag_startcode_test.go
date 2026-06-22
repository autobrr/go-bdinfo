package codec

import (
	"bytes"
	"strconv"
	"testing"
)

// nextStartCodeRef is the original byte-by-byte start-code scan that
// nextStartCode replaced. It is kept here as the reference oracle for
// differential testing so any divergence from the historical behavior is caught.
func nextStartCodeRef(data []byte, start int) (int, int) {
	for i := start; i+3 < len(data); i++ {
		if data[i] != 0x00 || data[i+1] != 0x00 {
			continue
		}
		if data[i+2] == 0x01 {
			return i, 3
		}
		if i+3 < len(data) && data[i+2] == 0x00 && data[i+3] == 0x01 {
			return i, 4
		}
	}
	return -1, 0
}

func TestNextStartCodeEquivalence_Cases(t *testing.T) {
	cases := [][]byte{
		nil,
		{},
		{0x00},
		{0x00, 0x00},
		{0x00, 0x00, 0x01},                   // 3-byte at end (loop bound rejects)
		{0x00, 0x00, 0x01, 0xFF},             // 3-byte, in range
		{0x00, 0x00, 0x00, 0x01},             // 4-byte
		{0x00, 0x00, 0x00, 0x01, 0xFF},       // 4-byte, in range
		{0x00, 0x00, 0x00, 0x00, 0x01},       // extra leading zero -> 4-byte one in
		{0x00, 0x00, 0x00, 0x00, 0x00, 0x01}, // many leading zeros
		{0xAA, 0x00, 0x00, 0x01, 0xBB},       // offset start code
		{0x00, 0x00, 0x02, 0x00, 0x00, 0x01}, // false prefix then real
		{0x00, 0x00, 0x01, 0x42, 0x00, 0x00, 0x01, 0x26}, // two start codes
		bytes.Repeat([]byte{0x00}, 12),
		bytes.Repeat([]byte{0x00, 0x00, 0x01, 0x55}, 5),
	}
	for ci, data := range cases {
		for start := 0; start <= len(data); start++ {
			gotP, gotL := nextStartCode(data, start)
			wantP, wantL := nextStartCodeRef(data, start)
			if gotP != wantP || gotL != wantL {
				t.Errorf("case %d start=%d data=%x: got (%d,%d) want (%d,%d)",
					ci, start, data, gotP, gotL, wantP, wantL)
			}
		}
	}
}

// FuzzNextStartCodeEquivalence exhaustively asserts the vectorized
// nextStartCode is identical to the original linear scan for every input and
// every valid start offset. This is the correctness guarantee that lets the
// rendered report stay byte-identical.
func FuzzNextStartCodeEquivalence(f *testing.F) {
	seeds := [][]byte{
		nil,
		{0x00, 0x00, 0x01},
		{0x00, 0x00, 0x00, 0x01},
		{0x00, 0x00, 0x00, 0x00, 0x01},
		{0xAA, 0x00, 0x00, 0x01, 0xBB},
		{0x00, 0x00, 0x01, 0x00, 0x00, 0x01},
		{0x00, 0x00, 0x00, 0x00, 0x00, 0x01},
		{0x00, 0x00, 0x01, 0x00, 0x00, 0x00, 0x01},
		{0x00, 0x00, 0x01, 0x00, 0x00},
	}
	for _, s := range seeds {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, data []byte) {
		for start := 0; start <= len(data); start++ {
			gotP, gotL := nextStartCode(data, start)
			wantP, wantL := nextStartCodeRef(data, start)
			if gotP != wantP || gotL != wantL {
				t.Fatalf("mismatch start=%d data=%x: got (%d,%d) want (%d,%d)",
					start, data, gotP, gotL, wantP, wantL)
			}
		}
	})
}

// buildFramePrefix mimics a UHD HEVC access-unit prefix as seen per frame in
// initialized mode: AUD, then sizeable non-VCL NAL bodies (SEI/RPU for DV/HDR)
// with emulation-prevention bytes, then the first VCL slice NAL. nextStartCode
// is scanned across all of this every frame to reach the slice.
func buildFramePrefix(seiBytes int) []byte {
	sc := []byte{0x00, 0x00, 0x01}
	var b []byte
	// AUD (type 35)
	b = append(b, sc...)
	b = append(b, 35<<1, 0x01, 0x50)
	// SEI prefix (type 39) with a body containing no start codes
	b = append(b, sc...)
	b = append(b, 39<<1, 0x01)
	body := make([]byte, seiBytes)
	for i := range body {
		// avoid accidental 00 00 0x sequences; emulation bytes appear as 00 00 03
		body[i] = byte(i*7+1) | 0x04
	}
	b = append(b, body...)
	// First VCL slice NAL (type 1): first_slice_segment_in_pic_flag=1, ...
	b = append(b, sc...)
	b = append(b, 1<<1, 0x01, 0xD8)
	return b
}

func benchScan(data []byte, scan func([]byte, int) (int, int)) int {
	total := 0
	pos, l := scan(data, 0)
	for pos != -1 {
		total += pos
		np, nl := scan(data, pos+l)
		if np == -1 {
			break
		}
		pos, l = np, nl
	}
	return total
}

func BenchmarkNextStartCode(b *testing.B) {
	for _, n := range []int{256, 2048, 8192} {
		data := buildFramePrefix(n)
		b.Run("old/sei="+strconv.Itoa(n), func(b *testing.B) {
			for b.Loop() {
				_ = benchScan(data, nextStartCodeRef)
			}
		})
		b.Run("new/sei="+strconv.Itoa(n), func(b *testing.B) {
			for b.Loop() {
				_ = benchScan(data, nextStartCode)
			}
		})
	}
}
