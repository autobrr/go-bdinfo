// Copyright (c) 2026, s0up and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package bdrom

import (
	"testing"

	"github.com/autobrr/go-bdinfo/internal/stream"
)

func benchmarkStreamData() []byte {
	data := make([]byte, 188*32768)
	for i := range 32768 {
		p := data[i*188 : (i+1)*188]
		p[0] = 0x47
		p[1] = 0x10
		p[2] = 0x11
		p[3] = 0x10 | byte(i&15)
		for j := 4; j < 188; j++ {
			p[j] = 0x55
		}
		if i == 0 {
			p[1] |= 0x40
			copy(p[4:], []byte{0, 0, 1, 0xe0, 0, 0, 0x80, 0, 0})
		}
	}
	return data
}

func BenchmarkStreamScanDisabled(b *testing.B) {
	data := benchmarkStreamData()
	b.SetBytes(int64(len(data)))
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		s := NewStreamFile(&memFileInfo{name: "BENCH.M2TS", data: data})
		s.Streams[0x1011] = &stream.VideoStream{Stream: stream.Stream{PID: 0x1011, StreamType: stream.StreamTypeHEVCVideo}}
		if err := s.Scan(nil, false); err != nil {
			b.Fatal(err)
		}
	}
}
