// Copyright (c) 2026, s0up and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package bdrom

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"hash/crc32"
	"io"
	"math/bits"
	"slices"
	"sync/atomic"
	"testing"

	"github.com/autobrr/go-bdinfo/internal/settings"
	"github.com/autobrr/go-bdinfo/internal/stream"
	"github.com/autobrr/go-bdinfo/pkg/bdinfo/video"
)

type recordingVideo struct {
	data             []byte
	boundaries       []video.PESStart
	breaks           []video.Discontinuity
	ends             []video.End
	consumeErr       error
	discontinuityErr error
	finishErr        error
	onConsume        func()
	chunkSizes       []int
	chunkStartCounts []int
}

func (r *recordingVideo) Consume(_ context.Context, c video.Chunk) error {
	r.data = append(r.data, c.Data...)
	for _, start := range c.Starts {
		start.Header = bytes.Clone(start.Header)
		r.boundaries = append(r.boundaries, start)
	}
	r.chunkSizes = append(r.chunkSizes, len(c.Data))
	r.chunkStartCounts = append(r.chunkStartCounts, len(c.Starts))
	if r.onConsume != nil {
		r.onConsume()
	}
	return r.consumeErr
}
func (r *recordingVideo) Discontinuity(_ context.Context, d video.Discontinuity) error {
	r.breaks = append(r.breaks, d)
	return r.discontinuityErr
}
func (r *recordingVideo) Finish(e video.End) error { r.ends = append(r.ends, e); return r.finishErr }

func collectionPacket(pid uint16, cc byte, start bool, payload []byte) []byte {
	p := make([]byte, 188)
	p[0] = 0x47
	p[1] = byte(pid >> 8)
	p[2] = byte(pid)
	p[3] = 0x30 | cc&15
	if len(payload) == 184 {
		p[3] = 0x10 | cc&15
		copy(p[4:], payload)
		if start {
			p[1] |= 0x40
		}
		return p
	}
	if start {
		p[1] |= 0x40
	}
	p[4] = byte(183 - len(payload))
	for j := 6; j < 188-len(payload); j++ {
		p[j] = 0xff
	}
	copy(p[188-len(payload):], payload)
	return p
}
func collectionPES(payload []byte, pts, dts uint64, bounded bool) []byte {
	h := []byte{0, 0, 1, 0xe0, 0, 0, 0x80, 0xc0, 10}
	p := encodePTS(0x30, pts)
	d := encodePTS(0x10, dts)
	h = append(h, p[:]...)
	h = append(h, d[:]...)
	if bounded {
		n := len(payload) + 13
		h[4] = byte(n >> 8)
		h[5] = byte(n)
	}
	return append(h, payload...)
}
func scanVideo(t *testing.T, data []byte) (*recordingVideo, []video.StreamResult) {
	t.Helper()
	r := &recordingVideo{}
	s := NewStreamFile(&memFileInfo{name: "TEST.M2TS", data: data})
	s.Streams[0x1011] = &stream.VideoStream{Stream: stream.Stream{PID: 0x1011, StreamType: stream.StreamTypeHEVCVideo}}
	s.VideoConsumer = func(context.Context, video.StreamInfo) (video.Consumer, error) { return r, nil }
	if err := s.Scan(nil, false); err != nil {
		t.Fatal(err)
	}
	return r, s.Collection
}
func TestVideoCollectionSplitPESAndOriginalTimestamps(t *testing.T) {
	for split := 1; split < 19; split++ {
		t.Run(string(rune('A'+split)), func(t *testing.T) {
			payload := []byte{0, 0, 0, 1, 0x4e, 1, 0x80}
			pes := collectionPES(payload, 0, (1<<33)-1, true)
			data := collectionPacket(0x1011, 15, true, pes[:split])
			data = append(data, collectionPacket(0x1011, 0, false, pes[split:])...)
			r, results := scanVideo(t, data)
			if !bytes.Equal(r.data, payload) {
				t.Fatalf("payload %x want %x", r.data, payload)
			}
			if len(r.boundaries) != 1 || !r.boundaries[0].HasPTS || r.boundaries[0].PTS != 0 || r.boundaries[0].DTS != (1<<33)-1 || r.boundaries[0].PacketIndex != 0 {
				t.Fatalf("boundaries %+v", r.boundaries)
			}
			if len(r.ends) != 1 || !r.ends[0].CleanEOF || len(results) != 1 || results[0].Status != video.Complete {
				t.Fatalf("ends %+v results %+v", r.ends, results)
			}
		})
	}
}
func TestVideoCollectionBeyondProbeAndDuplicate(t *testing.T) {
	payload := bytes.Repeat([]byte{0x55}, maxStreamDataVideo+777)
	pes := collectionPES(payload, 90, 45, false)
	var data []byte
	for i, cc := 0, byte(0); i < len(pes); cc++ {
		n := min(184, len(pes)-i)
		p := collectionPacket(0x1011, cc, i == 0, pes[i:i+n])
		data = append(data, p...)
		if i == 0 {
			data = append(data, p...)
		}
		i += n
	}
	r, _ := scanVideo(t, data)
	if !bytes.Equal(r.data, payload) {
		t.Fatalf("delivered %d want %d", len(r.data), len(payload))
	}
	if !r.ends[0].CleanEOF {
		t.Fatalf("end %+v", r.ends)
	}
}
func TestVideoCollectionDamageIsIncomplete(t *testing.T) {
	p := collectionPES([]byte{1, 2, 3}, 1, 1, true)
	data := collectionPacket(0x1011, 0, true, p)
	data = append(data, collectionPacket(0x1011, 2, true, p)...)
	r, res := scanVideo(t, data)
	if len(r.breaks) != 1 || res[0].Status != video.Incomplete || r.ends[0].CleanEOF {
		t.Fatalf("breaks %+v result %+v end %+v", r.breaks, res, r.ends)
	}
}

func TestVideoCollectionAnnouncedRetransmission(t *testing.T) {
	for _, pcr := range []bool{false, true} {
		t.Run(fmt.Sprint("PCR=", pcr), func(t *testing.T) {
			p := collectionPacket(0x1011, 8, true, collectionPES([]byte{1, 2}, 1, 1, true))
			p[5] = 0x80
			q := append([]byte(nil), p...)
			if pcr {
				p[5], q[5] = 0x90, 0x90
				copy(p[6:12], []byte{0, 0, 0, 0, 0x7e, 0})
				copy(q[6:12], p[6:12])
				q[6] = 0x10 // Retransmissions may update only the PCR value.
			}
			data := append(append([]byte(nil), p...), q...)
			r, res := scanVideo(t, data)
			if !bytes.Equal(r.data, []byte{1, 2}) || len(r.boundaries) != 1 || len(r.breaks) != 1 || res[0].Status != video.Complete {
				t.Fatalf("retransmission: data %x boundaries %+v breaks %+v result %+v", r.data, r.boundaries, r.breaks, res)
			}
			_, res = scanVideo(t, append(data, q...))
			if res[0].Status == video.Complete || !errors.Is(res[0].Err, video.ErrInvalidTransport) {
				t.Fatalf("third retransmission completed: %+v", res)
			}
		})
	}
}

func TestVideoCollectionAnnouncedContinuation(t *testing.T) {
	for _, adaptationOnly := range []bool{false, true} {
		t.Run(fmt.Sprint("adaptationOnly=", adaptationOnly), func(t *testing.T) {
			data := collectionPacket(0x1011, 0, true, collectionPES([]byte{1}, 1, 1, false))
			reset := collectionPacket(0x1011, 8, false, []byte{2})
			if adaptationOnly {
				reset = collectionPacket(0x1011, 8, false, nil)
				reset[3] = 0x28
			}
			reset[5] = 0x80
			data = append(data, reset...)
			if adaptationOnly {
				// A reset at EOF need not lose the already delivered unbounded PES.
				r, res := scanVideo(t, data)
				if !bytes.Equal(r.data, []byte{1}) || len(r.breaks) != 1 || r.breaks[0].DataLoss || res[0].Status != video.Complete {
					t.Fatalf("reset EOF: data %x breaks %+v result %+v", r.data, r.breaks, res)
				}
				data = append(data, collectionPacket(0x1011, 9, false, []byte{2})...)
			}
			cc := byte(9)
			if adaptationOnly {
				cc++
			}
			data = append(data, collectionPacket(0x1011, cc, true, collectionPES([]byte{3}, 2, 2, true))...)
			r, res := scanVideo(t, data)
			if !bytes.Equal(r.data, []byte{1, 2, 3}) || len(r.breaks) != 1 || r.breaks[0].DataLoss || len(r.boundaries) != 2 || r.boundaries[1].Segment != 1 || res[0].Status != video.Complete {
				t.Fatalf("continuation: data %x boundaries %+v breaks %+v result %+v", r.data, r.boundaries, r.breaks, res)
			}
		})
	}
	for _, adaptationOnly := range []bool{false, true} {
		reset := collectionPacket(0x1011, 8, false, []byte{2})
		if adaptationOnly {
			reset = collectionPacket(0x1011, 8, false, nil)
			reset[3] = 0x28
		}
		reset[5] = 0x80
		data := reset
		cc := byte(9)
		if adaptationOnly {
			data = append(data, collectionPacket(0x1011, cc, false, []byte{2})...)
			cc++
		}
		data = append(data, collectionPacket(0x1011, cc, true, collectionPES([]byte{3}, 2, 2, true))...)
		_, res := scanVideo(t, data)
		if res[0].Status == video.Complete || !errors.Is(res[0].Err, video.ErrInvalidTransport) {
			t.Fatalf("unknown PES after announced reset completed: %+v", res)
		}
	}
}

func TestVideoCollectionTimestampOrderAndEventOnlyEOF(t *testing.T) {
	var data []byte
	for i, pts := range []uint64{(1 << 33) - 1, 0, 9000, 4500} {
		p := collectionPES([]byte{byte(i)}, pts, uint64(i), true)
		if i == 3 {
			p = collectionPES(nil, pts, uint64(i), true)
		}
		data = append(data, collectionPacket(0x1011, byte(i), true, p)...)
	}
	r, res := scanVideo(t, data)
	if len(r.boundaries) != 4 || r.boundaries[0].PTS != (1<<33)-1 || r.boundaries[1].PTS != 0 || r.boundaries[3].PTS != 4500 || r.boundaries[3].Offset != 3 || res[0].Boundaries != 4 || res[0].Status != video.Complete {
		t.Fatalf("result %+v boundaries %+v", res, r.boundaries)
	}
	if len(r.chunkSizes) != 2 || r.chunkSizes[1] != 0 || r.chunkStartCounts[1] != 1 {
		t.Fatalf("final empty PES not in event-only chunk: sizes %v starts %v", r.chunkSizes, r.chunkStartCounts)
	}
}

func TestVideoCollectionTransportCases(t *testing.T) {
	p := collectionPES([]byte{1}, 0, 0, true)
	base := collectionPacket(0x1011, 0, true, p)
	tests := []struct {
		name     string
		change   func([]byte) []byte
		complete bool
	}{
		{"bounded padding", func(b []byte) []byte { return append(b, collectionPacket(0x1011, 1, false, []byte{0xff, 0xff})...) }, true},
		{"M2TS", func(b []byte) []byte {
			return append(append([]byte{1, 2, 3, 4}, b...), append([]byte{1, 2, 3, 4}, collectionPacket(0x1fff, 0, false, []byte{0})...)...)
		}, true},
		{"adaptation only", func(b []byte) []byte {
			a := make([]byte, 188)
			a[0] = 0x47
			a[1] = 0x10
			a[2] = 0x11
			a[3] = 0x20
			a[4] = 183
			for j := 6; j < len(a); j++ {
				a[j] = 0xff
			}
			return append(b, a...)
		}, true},
		{"announced reset", func(b []byte) []byte { q := collectionPacket(0x1011, 8, true, p); q[5] = 0x80; return append(b, q...) }, true},
		{"announced interrupted header", func([]byte) []byte {
			b := collectionPacket(0x1011, 0, true, p[:2])
			q := collectionPacket(0x1011, 8, true, p)
			q[5] = 0x80
			return append(b, q...)
		}, false},
		{"announced interrupted bounded PES", func([]byte) []byte {
			h := append([]byte(nil), p...)
			h[5] += 2
			b := collectionPacket(0x1011, 0, true, h)
			q := collectionPacket(0x1011, 8, true, p)
			q[5] = 0x80
			return append(b, q...)
		}, false},
		{"partial packet", func(b []byte) []byte { return append(b, base[:10]...) }, false},
		{"same counter changed", func(b []byte) []byte { q := append([]byte(nil), b...); q[187]++; return append(b, q...) }, false},
		{"third duplicate", func(b []byte) []byte { return append(append(b, base...), base...) }, false},
		{"TEI", func(b []byte) []byte { b[1] |= 0x80; return append(b, collectionPacket(0x1011, 1, true, p)...) }, false},
		{"scrambling", func(b []byte) []byte { b[3] |= 0x80; return append(b, collectionPacket(0x1011, 1, true, p)...) }, false},
		{"invalid adaptation", func(b []byte) []byte { b[4] = 184; return append(b, collectionPacket(0x1011, 1, true, p)...) }, false},
		{"partial header", func(b []byte) []byte { return append(b, collectionPacket(0x1011, 1, true, p[:2])...) }, false},
		{"unfinished bounded PES", func(b []byte) []byte {
			q := append([]byte(nil), p...)
			q[5] += 2
			return append(b, collectionPacket(0x1011, 1, true, q)...)
		}, false},
		{"bad timestamp marker", func(b []byte) []byte {
			q := append([]byte(nil), p...)
			q[9] &= 0xfe
			return append(b, collectionPacket(0x1011, 1, true, q)...)
		}, false},
		{"missing PES start", func(b []byte) []byte {
			b[1] &= 0xbf
			return append(b, collectionPacket(0x1fff, 1, false, []byte{0})...)
		}, false},
		{"source framing", func(b []byte) []byte { q := append([]byte(nil), b...); q[0] = 0; return append(b, q...) }, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			data := tc.change(append([]byte(nil), base...))
			r, res := scanVideo(t, data)
			if len(r.ends) != 1 || r.ends[0].CleanEOF != tc.complete || (res[0].Status == video.Complete) != tc.complete {
				t.Fatalf("ends %+v result %+v", r.ends, res)
			}
		})
	}
}

func TestVideoCollectionFactoryAndCallbackFailures(t *testing.T) {
	cause := errors.New("collector failure")
	finishCause := errors.New("finish failure")
	for _, mode := range []string{"decline", "factory", "factory with consumer", "consume", "finish", "cancel"} {
		t.Run(mode, func(t *testing.T) {
			p := collectionPES([]byte{1}, 1, 1, true)
			data := append(collectionPacket(0x1011, 0, true, p), collectionPacket(0x1011, 1, true, p)...)
			s := NewStreamFile(&memFileInfo{name: "TEST.M2TS", data: data})
			s.Streams[0x1011] = &stream.VideoStream{Stream: stream.Stream{PID: 0x1011, StreamType: stream.StreamTypeHEVCVideo}}
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			r := &recordingVideo{}
			calls := 0
			s.VideoConsumer = func(context.Context, video.StreamInfo) (video.Consumer, error) {
				calls++
				switch mode {
				case "decline":
					return nil, nil
				case "factory":
					return nil, cause
				case "factory with consumer":
					r.finishErr = finishCause
					return r, cause
				case "consume":
					r.consumeErr = cause
					r.finishErr = finishCause
				case "finish":
					r.finishErr = finishCause
				case "cancel":
					r.onConsume = cancel
				}
				return r, nil
			}
			err := s.ScanWithProgress(ctx, nil, false, nil)
			if err != nil && !errors.Is(err, context.Canceled) {
				t.Fatal(err)
			}
			if calls != 1 || len(s.Collection) != 1 {
				t.Fatalf("calls %d results %+v", calls, s.Collection)
			}
			result := s.Collection[0]
			if mode == "decline" {
				if len(r.ends) != 0 || result.Status != video.Declined {
					t.Fatal(result)
				}
				return
			}
			if mode == "factory" {
				if len(r.ends) != 0 || !errors.Is(result.Err, cause) {
					t.Fatal(result)
				}
				return
			}
			if len(r.ends) != 1 {
				t.Fatalf("Finish called %d times", len(r.ends))
			}
			if mode == "cancel" {
				if result.Status != video.Canceled || r.ends[0].CleanEOF {
					t.Fatal(result, r.ends)
				}
				return
			}
			if result.Status != video.Failed || !errors.Is(result.Err, finishCause) {
				t.Fatal(result)
			}
			if mode != "finish" && !errors.Is(result.Err, cause) {
				t.Fatal(result)
			}
		})
	}
}

func TestVideoCollectionBoundaryOnlyBatches(t *testing.T) {
	// Timestamp absence and zero-length PES payloads still carry original starts.
	pes := []byte{0, 0, 1, 0xe0, 0, 3, 0x80, 0, 0}
	var data []byte
	for i := range 600 {
		data = append(data, collectionPacket(0x1011, byte(i&15), true, pes)...)
	}
	r, res := scanVideo(t, data)
	if res[0].Status != video.Complete || len(r.data) != 0 || len(r.boundaries) != 600 {
		t.Fatalf("result %+v boundaries %d", res, len(r.boundaries))
	}
	for i, start := range r.boundaries {
		if start.Offset != 0 || start.HasPTS || start.HasDTS || start.PacketIndex != uint64(i) {
			t.Fatal(start)
		}
	}
	for _, count := range r.chunkStartCounts {
		if count > collectionBoundaryLimit {
			t.Fatalf("unbounded boundary batch: %d", count)
		}
	}
}

func TestVideoCollectionHeadersSurviveBatchReuse(t *testing.T) {
	var data, wantPayload []byte
	var wantHeaders [][]byte
	cc := byte(0)
	for i := range 800 {
		var payload []byte
		switch {
		case i == 0 || i == 350:
			payload = bytes.Repeat([]byte{byte(i)}, collectionBatchSize+17)
		case i%37 == 0:
			payload = []byte{byte(i)}
		}
		wantPayload = append(wantPayload, payload...)
		header := collectionPES(nil, uint64(i*3003), uint64(i*3000), false)
		// Different header sizes exercise compaction, including the 264-byte maximum.
		header = append(header, bytes.Repeat([]byte{0xff}, i%246)...)
		header[8] = byte(len(header) - 9)
		wantHeaders = append(wantHeaders, bytes.Clone(header))
		pes := append(header, payload...)
		for start := true; len(pes) > 0; start = false {
			n := min(184, len(pes))
			data = append(data, collectionPacket(0x1011, cc, start, pes[:n])...)
			cc++
			pes = pes[n:]
		}
	}
	r, results := scanVideo(t, data)
	if results[0].Status != video.Complete || !bytes.Equal(r.data, wantPayload) || len(r.boundaries) != len(wantHeaders) {
		t.Fatalf("result %+v, bytes %d/%d, boundaries %d/%d", results, len(r.data), len(wantPayload), len(r.boundaries), len(wantHeaders))
	}
	for i, start := range r.boundaries {
		if !bytes.Equal(start.Header, wantHeaders[i]) {
			t.Fatalf("header %d: got %x, want %x", i, start.Header, wantHeaders[i])
		}
	}
}

func TestVideoCollectionFailureIsolationAndUnobservedPID(t *testing.T) {
	cause := errors.New("reset callback failure")
	s := NewStreamFile(&memFileInfo{name: "TEST.M2TS"})
	recorders := make(map[uint16]*recordingVideo)
	for _, pid := range []uint16{0x1011, 0x1012, 0x1013} {
		s.Streams[pid] = &stream.VideoStream{Stream: stream.Stream{PID: pid, StreamType: stream.StreamTypeHEVCVideo}}
		recorders[pid] = &recordingVideo{}
	}
	recorders[0x1011].discontinuityErr = cause
	s.VideoConsumer = func(_ context.Context, info video.StreamInfo) (video.Consumer, error) {
		return recorders[info.PID], nil
	}
	c := s.newVideoCollection(t.Context(), nil, streamSource(s, false))
	pes := collectionPES([]byte{1}, 0, 0, true)
	c.packet(collectionPacket(0x1011, 0, true, pes), 188, 0)
	c.packet(collectionPacket(0x1011, 2, true, pes), 188, 0)
	c.packet(collectionPacket(0x1012, 0, true, pes), 188, 0)
	res := c.finish(nil, true)
	if res[0].Status != video.Failed || !errors.Is(res[0].Err, cause) || res[1].Status != video.Complete || res[2].Status != video.Incomplete {
		t.Fatal(res)
	}
	for _, r := range recorders {
		if len(r.ends) != 1 {
			t.Fatalf("Finish count %d", len(r.ends))
		}
	}
}

type discardVideo struct {
	bytes    uint64
	complete bool
}

func (r *discardVideo) Consume(ctx context.Context, c video.Chunk) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	r.bytes += uint64(len(c.Data))
	return nil
}
func (r *discardVideo) Discontinuity(context.Context, video.Discontinuity) error { return nil }
func (r *discardVideo) Finish(e video.End) error                                 { r.complete = e.CleanEOF; return nil }

func BenchmarkVideoCollection(b *testing.B) {
	data := benchmarkStreamData()
	b.SetBytes(int64(len(data)))
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		s := NewStreamFile(&memFileInfo{name: "BENCH.M2TS", data: data})
		s.Streams[0x1011] = &stream.VideoStream{Stream: stream.Stream{PID: 0x1011, StreamType: stream.StreamTypeHEVCVideo}}
		r := &discardVideo{}
		s.VideoConsumer = func(context.Context, video.StreamInfo) (video.Consumer, error) { return r, nil }
		if err := s.Scan(nil, false); err != nil {
			b.Fatal(err)
		}
		if !r.complete || r.bytes != uint64(184*32768-9) || len(s.Collection) != 1 || s.Collection[0].Status != video.Complete {
			b.Fatal("collection incomplete")
		}
	}
}

func TestCollectionReaderPassCount(t *testing.T) {
	data := benchmarkStreamData()
	var baselineReads []int
	for _, enabled := range []bool{false, true} {
		fi := &collectionReadFile{memFileInfo: memFileInfo{name: "TEST.M2TS", data: data}, err: io.EOF, reportedLength: int64(len(data))}
		s := NewStreamFile(fi)
		s.Streams[0x1011] = &stream.VideoStream{Stream: stream.Stream{PID: 0x1011, StreamType: stream.StreamTypeHEVCVideo}}
		if enabled {
			s.VideoConsumer = func(context.Context, video.StreamInfo) (video.Consumer, error) { return &discardVideo{}, nil }
		}
		if err := s.Scan(nil, false); err != nil {
			t.Fatal(err)
		}
		if fi.opens != 3 || fi.closes != 3 {
			t.Fatalf("enabled=%v opens=%d closes=%d (two existing PMT probes and one full scan)", enabled, fi.opens, fi.closes)
		}
		if !enabled {
			if fi.sourceNameCalls.Load() != 0 {
				t.Fatal("disabled collection constructed a source identity")
			}
			baselineReads = append([]int(nil), fi.readBytes...)
		} else if !slices.Equal(baselineReads, fi.readBytes) {
			t.Fatalf("collection added reads: baseline %v enabled %v", baselineReads, fi.readBytes)
		}
	}
}

func TestCollectionSharedPlaylistReaderPassCount(t *testing.T) {
	packets := benchmarkStreamData()
	copy(packets[2*188:3*188], packets[:188])
	packets[2*188+3] = 0x12
	pat := psiWithCRC([]byte{0, 0xb0, 13, 0, 1, 0xc1, 0, 0, 0, 1, 0xf0, 0})
	pmt := psiWithCRC([]byte{2, 0xb0, 18, 0, 1, 0xc1, 0, 0, 0xf0, 0x11, 0xf0, 0, 0x24, 0xf0, 0x11, 0xf0, 0})
	copy(packets, collectionPacket(0, 0, true, append([]byte{0}, pat...)))
	copy(packets[188:], collectionPacket(0x1000, 0, true, append([]byte{0}, pmt...)))
	var data []byte
	for at := 0; at < len(packets); at += 188 {
		data = append(data, 0, 0, 0, 0)
		data = append(data, packets[at:at+188]...)
	}
	clpi := make([]byte, 60)
	copy(clpi, "HDMV0200")
	binary.BigEndian.PutUint32(clpi[8:], 60)
	binary.BigEndian.PutUint32(clpi[40:], 16)
	binary.BigEndian.PutUint32(clpi[56:], uint32(len(data)/192))
	sequence := make([]byte, 22)
	sequence[1], sequence[6], sequence[7] = 1, 1, 7
	binary.BigEndian.PutUint16(sequence[8:], 0x1001)
	binary.BigEndian.PutUint32(sequence[18:], 90000)
	clpi = append(binary.BigEndian.AppendUint32(clpi, uint32(len(sequence))), sequence...)
	binary.BigEndian.PutUint32(clpi[12:], uint32(len(clpi)))
	program := make([]byte, 18)
	program[1], program[8] = 1, 1
	copy(program[10:], []byte{0x10, 0x11, 4, 0x24, 0x61, 0x30, 0})
	clpi = append(binary.BigEndian.AppendUint32(clpi, uint32(len(program))), program...)
	var baselineReads []int
	for _, enabled := range []bool{false, true} {
		fi := &collectionReadFile{memFileInfo: memFileInfo{name: "00001.M2TS", data: data}, err: io.EOF, reportedLength: int64(len(data))}
		sf := NewStreamFile(fi)
		cf := NewStreamClipFile(&memFileInfo{name: "00001.CLPI", data: clpi})
		bd := &BDROM{
			CaptureTimeline: enabled,
			StreamFiles:     map[string]*StreamFile{sf.Name: sf},
			StreamClipFiles: map[string]*StreamClipFile{cf.Name: cf},
			PlaylistFiles:   make(map[string]*PlaylistFile),
		}
		for _, name := range []string{"00000.MPLS", "00001.MPLS"} {
			bd.PlaylistFiles[name] = NewPlaylistFile(&memFileInfo{name: name, data: timelineFixture(1, 45001, 2)}, settings.Settings{})
		}
		consumer := &discardVideo{}
		calls := 0
		if enabled {
			bd.VideoConsumer = func(_ context.Context, info video.StreamInfo) (video.Consumer, error) {
				calls++
				if len(info.Occurrences) != 4 {
					t.Fatalf("occurrences: %+v", info.Occurrences)
				}
				return consumer, nil
			}
		}
		result := bd.ScanWithProgress(t.Context(), nil)
		if result.ScanError != nil || len(result.FileErrors) != 0 {
			t.Fatalf("scan: %+v", result)
		}
		fullReads := 0
		for _, n := range fi.readBytes {
			if n == len(data) {
				fullReads++
			}
		}
		if fullReads != 1 || fi.opens != fi.closes {
			t.Fatalf("enabled=%v full passes=%d bytes=%v opens/closes=%d/%d", enabled, fullReads, fi.readBytes, fi.opens, fi.closes)
		}
		if !enabled {
			baselineReads = slices.Clone(fi.readBytes)
			continue
		}
		if !slices.Equal(baselineReads, fi.readBytes) || calls != 1 || !consumer.complete || consumer.bytes != uint64((32768-2)*184-9) {
			t.Fatalf("reads %v baseline %v calls %d consumer %+v", fi.readBytes, baselineReads, calls, consumer)
		}
		for _, p := range bd.PlaylistFiles {
			if len(p.TimelineItems) != 2 || p.TimelineErr != nil {
				t.Fatalf("playlist %s items=%d err=%v", p.Name, len(p.TimelineItems), p.TimelineErr)
			}
		}
	}
}

func psiWithCRC(section []byte) []byte {
	rev := make([]byte, len(section))
	for i, v := range section {
		rev[i] = bits.Reverse8(v)
	}
	sum := bits.Reverse32(^crc32.ChecksumIEEE(rev))
	return binary.BigEndian.AppendUint32(section, sum)
}

func TestCollectionPMTRemappingIsVisible(t *testing.T) {
	pat := psiWithCRC([]byte{0, 0xb0, 13, 0, 1, 0xc1, 0, 0, 0, 1, 0xf0, 0})
	pmt := psiWithCRC([]byte{2, 0xb0, 18, 0, 1, 0xc1, 0, 0, 0xf0, 0x11, 0xf0, 0, 0x24, 0xf0, 0x11, 0xf0, 0})
	replacement := psiWithCRC([]byte{2, 0xb0, 18, 0, 1, 0xc3, 0, 0, 0xf0, 0x12, 0xf0, 0, 0x24, 0xf0, 0x12, 0xf0, 0})
	data := collectionPacket(0, 0, true, append([]byte{0}, pat...))
	data = append(data, collectionPacket(0x1000, 0, true, append([]byte{0}, pmt...))...)
	data = append(data, collectionPacket(0x1011, 0, true, collectionPES([]byte{1}, 0, 0, true))...)
	data = append(data, collectionPacket(0x1000, 1, true, append([]byte{0}, replacement...))...)
	r, res := scanVideo(t, data)
	if res[0].Status == video.Complete || r.ends[0].CleanEOF || !errors.Is(res[0].Err, video.ErrUnsupportedMapping) {
		t.Fatalf("remapping result %+v end %+v", res, r.ends)
	}
	// An unchanged valid PMT must not taint the collection.
	copy(data[len(data)-188:], collectionPacket(0x1000, 1, true, append([]byte{0}, pmt...)))
	r, res = scanVideo(t, data)
	if res[0].Status != video.Complete || !r.ends[0].CleanEOF {
		t.Fatal(res)
	}
	data[len(data)-1] ^= 1
	r, res = scanVideo(t, data)
	if res[0].Status == video.Complete || r.ends[0].CleanEOF {
		t.Fatal("invalid PMT CRC accepted")
	}
}

func TestCollectionRequiresUnambiguousProgramTables(t *testing.T) {
	for _, mode := range []string{"unique", "network entry", "distinct PMT PIDs", "program two", "duplicate PMT matching first", "duplicate PMT matching last", "duplicate PMT identical", "duplicate PAT identical", "duplicate PAT conflict", "multiple PAT programs", "PMT program conflict"} {
		t.Run(mode, func(t *testing.T) {
			pat := []byte{0, 0xb0, 13, 0, 1, 0xc1, 0, 0, 0, 1, 0xf0, 0}
			pmt := []byte{2, 0xb0, 18, 0, 1, 0xc1, 0, 0, 0xf0, 0x11, 0xf0, 0, 0x24, 0xf0, 0x11, 0xf0, 0}
			valid := mode == "unique" || mode == "network entry" || mode == "distinct PMT PIDs" || mode == "program two"
			switch mode {
			case "distinct PMT PIDs":
				pmt = append(pmt, 0x24, 0xf0, 0x12, 0xf0, 0)
				pmt[2] += 5
			case "program two":
				pat[9], pmt[4] = 2, 2
			case "network entry", "duplicate PAT identical", "duplicate PAT conflict", "multiple PAT programs":
				entry := []byte{0, 1, 0xf0, 0}
				if mode == "network entry" {
					entry[1] = 0
				} else if mode == "duplicate PAT conflict" {
					entry[3] = 1
				} else if mode == "multiple PAT programs" {
					entry[1] = 2
				}
				pat = append(pat, entry...)
				pat[2] += 4
			case "duplicate PMT matching first", "duplicate PMT matching last", "duplicate PMT identical":
				pmt = append(pmt, pmt[12:17]...)
				pmt[2] += 5
				if mode == "duplicate PMT matching first" {
					pmt[17] = 0x1b
				} else if mode == "duplicate PMT matching last" {
					pmt[12] = 0x1b
				}
			case "PMT program conflict":
				pmt[4] = 2
			}
			pat, pmt = psiWithCRC(pat), psiWithCRC(pmt)
			data := collectionPacket(0, 0, true, append([]byte{0}, pat...))
			data = append(data, collectionPacket(0x1000, 0, true, append([]byte{0}, pmt...))...)
			data = append(data, collectionPacket(0x1011, 0, true, collectionPES([]byte{1}, 0, 0, true))...)
			r, res := scanVideo(t, data)
			if (res[0].Status == video.Complete) != valid || r.ends[0].CleanEOF != valid {
				t.Fatalf("ambiguous program table: result %+v end %+v", res, r.ends)
			}
		})
	}
}

func TestCollectionZeroLengthAdaptationExtension(t *testing.T) {
	for _, pid := range []uint16{0, 0x1000, 0x1011} {
		for _, length := range []byte{0, 1, 255} {
			t.Run(fmt.Sprintf("PID-%x-length-%d", pid, length), func(t *testing.T) {
				pat := psiWithCRC([]byte{0, 0xb0, 13, 0, 1, 0xc1, 0, 0, 0, 1, 0xf0, 0})
				pmt := psiWithCRC([]byte{2, 0xb0, 18, 0, 1, 0xc1, 0, 0, 0xf0, 0x11, 0xf0, 0, 0x24, 0xf0, 0x11, 0xf0, 0})
				packets := [][]byte{
					collectionPacket(0, 0, true, append([]byte{0}, pat...)),
					collectionPacket(0x1000, 0, true, append([]byte{0}, pmt...)),
					collectionPacket(0x1011, 0, true, collectionPES([]byte{1}, 0, 0, true)),
				}
				for _, p := range packets {
					if uint16(p[1]&31)<<8|uint16(p[2]) == pid {
						for i := 7; i < 5+int(p[4]); i++ {
							p[i] = 0xff
						}
						p[5], p[6] = 1, length
						if length == 1 {
							p[7] = 0x1f
						}
					}
				}
				r, res := scanVideo(t, bytes.Join(packets, nil))
				valid := length == 1
				if (res[0].Status == video.Complete) != valid || r.ends[0].CleanEOF != valid || valid && !bytes.Equal(r.data, []byte{1}) {
					t.Fatalf("optional extension: result %+v bytes %x end %+v", res, r.data, r.ends)
				}
			})
		}
	}
}

func TestCollectionAcceptsZeroLengthOuterAdaptation(t *testing.T) {
	payload := bytes.Repeat([]byte{1}, 164)
	packet := collectionPacket(0x1011, 0, true, collectionPES(payload, 0, 0, true))
	if packet[4] != 0 {
		t.Fatal("fixture must have zero outer adaptation length")
	}
	r, res := scanVideo(t, append(packet, collectionPacket(0x1fff, 0, false, []byte{0})...))
	if res[0].Status != video.Complete || !r.ends[0].CleanEOF || !bytes.Equal(r.data, payload) {
		t.Fatalf("legal empty outer field rejected: %+v bytes %x end %+v", res, r.data, r.ends)
	}
}

func TestCollectionPSIAnnouncedRetransmission(t *testing.T) {
	pat := psiWithCRC([]byte{0, 0xb0, 13, 0, 1, 0xc1, 0, 0, 0, 1, 0xf0, 0})
	pmt := psiWithCRC([]byte{2, 0xb0, 18, 0, 1, 0xc1, 0, 0, 0xf0, 0x11, 0xf0, 0, 0x24, 0xf0, 0x11, 0xf0, 0})
	for _, pid := range []uint16{0, 0x1000} {
		t.Run(fmt.Sprint(pid), func(t *testing.T) {
			data := collectionPacket(0, 0, true, append([]byte{0}, pat...))
			data = append(data, collectionPacket(0x1000, 0, true, append([]byte{0}, pmt...))...)
			data = append(data, collectionPacket(0x1011, 0, true, collectionPES([]byte{1}, 0, 0, true))...)
			section := pat
			if pid != 0 {
				section = pmt
			}
			reset := collectionPacket(pid, 8, true, append([]byte{0}, section...))
			reset[5] = 0x80
			data = append(append(data, reset...), reset...)
			data = append(data, collectionPacket(0x1011, 1, true, collectionPES([]byte{2}, 1, 1, true))...)
			r, res := scanVideo(t, data)
			if !bytes.Equal(r.data, []byte{1, 2}) || len(r.breaks) != 1 || res[0].Status != video.Incomplete {
				t.Fatalf("mapping reset retransmission: data %x breaks %+v result %+v", r.data, r.breaks, res)
			}
		})
	}
}

type collectionReadFile struct {
	memFileInfo
	err             error
	reportedLength  int64
	opens, closes   int
	readBytes       []int
	sourceNameCalls atomic.Int64
}

func (f *collectionReadFile) Length() int64 { return f.reportedLength }
func (f *collectionReadFile) FullName() string {
	f.sourceNameCalls.Add(1)
	return f.memFileInfo.FullName()
}
func (f *collectionReadFile) OpenRead() (io.ReadCloser, error) {
	f.opens++
	f.readBytes = append(f.readBytes, 0)
	return &collectionReader{file: f, index: len(f.readBytes) - 1}, nil
}

type collectionReader struct {
	file  *collectionReadFile
	pos   int
	index int
}

func (r *collectionReader) Read(b []byte) (int, error) {
	n := copy(b, r.file.data[r.pos:])
	r.pos += n
	r.file.readBytes[r.index] += n
	if r.pos == len(r.file.data) {
		return n, r.file.err
	}
	return n, nil
}
func (r *collectionReader) Close() error { r.file.closes++; return nil }

func TestVideoCollectionReadTermination(t *testing.T) {
	cause := errors.New("source read error")
	p := collectionPES([]byte{1}, 1, 1, true)
	for _, tc := range []struct {
		name        string
		err         error
		extraLength int64
		short       bool
		complete    bool
	}{{"data with EOF", io.EOF, 0, false, true}, {"data with error", cause, 0, false, false}, {"length mismatch", io.EOF, 188, false, false}, {"short input", io.EOF, 0, true, false}} {
		t.Run(tc.name, func(t *testing.T) {
			data := append(collectionPacket(0x1011, 0, true, p), collectionPacket(0x1011, 1, true, p)...)
			if tc.short {
				data = data[:100]
			}
			fi := &collectionReadFile{memFileInfo: memFileInfo{name: "TEST.M2TS", data: data}, err: tc.err, reportedLength: int64(len(data)) + tc.extraLength}
			s := NewStreamFile(fi)
			s.Streams[0x1011] = &stream.VideoStream{Stream: stream.Stream{PID: 0x1011, StreamType: stream.StreamTypeHEVCVideo}}
			r := &recordingVideo{}
			s.VideoConsumer = func(context.Context, video.StreamInfo) (video.Consumer, error) { return r, nil }
			_ = s.Scan(nil, false)
			if len(r.ends) != 1 || r.ends[0].CleanEOF != tc.complete || fi.opens != fi.closes {
				t.Fatalf("end %+v opens %d closes %d", r.ends, fi.opens, fi.closes)
			}
			if tc.err == cause && (!errors.Is(s.Collection[0].Err, cause) || !bytes.Equal(r.data, []byte{1, 1})) {
				t.Fatalf("data %v result %+v", r.data, s.Collection)
			}
		})
	}
}

func TestVideoCollectionSinglePacketEOF(t *testing.T) {
	t.Run("report scanner accepts whole packet", func(t *testing.T) {
		data := collectionPacket(0x1011, 0, true, collectionPES([]byte{1, 2, 3}, 0, 0, true))
		s := NewStreamFile(&memFileInfo{name: "TEST.M2TS", data: data})
		if err := s.Scan(nil, false); err != nil {
			t.Fatalf("whole TS packet: %v", err)
		}
	})
	cause := errors.New("source failure")
	for _, tc := range []struct {
		name        string
		tail        int
		readErr     error
		extraLength int64
		complete    bool
	}{
		{"whole packet", 0, io.EOF, 0, true},
		{"one trailing byte", 1, io.EOF, 0, false},
		{"two trailing bytes", 2, io.EOF, 0, false},
		{"three trailing bytes", 3, io.EOF, 0, false},
		{"read failure", 0, cause, 0, false},
		{"declared extent missing", 0, io.EOF, 188, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			data := collectionPacket(0x1011, 0, true, collectionPES([]byte{1, 2, 3}, 0, 0, true))
			data = append(data, make([]byte, tc.tail)...)
			fi := &collectionReadFile{memFileInfo: memFileInfo{name: "TEST.M2TS", data: data}, err: tc.readErr, reportedLength: int64(len(data)) + tc.extraLength}
			s := NewStreamFile(fi)
			s.Streams[0x1011] = &stream.VideoStream{Stream: stream.Stream{PID: 0x1011, StreamType: stream.StreamTypeHEVCVideo}}
			r := &recordingVideo{}
			s.VideoConsumer = func(context.Context, video.StreamInfo) (video.Consumer, error) { return r, nil }
			_ = s.Scan(nil, false)
			if !bytes.Equal(r.data, []byte{1, 2, 3}) || len(r.ends) != 1 || len(s.Collection) != 1 || fi.opens != fi.closes {
				t.Fatalf("data %v ends %+v results %+v opens/closes %d/%d", r.data, r.ends, s.Collection, fi.opens, fi.closes)
			}
			end, result := r.ends[0], s.Collection[0]
			if tc.complete {
				if !end.CleanEOF || end.Err != nil || end.SourcePacketCount != 1 || result.Status != video.Complete || result.Err != nil || result.SourcePacketCount != 1 {
					t.Fatalf("complete packet: end %+v result %+v", end, result)
				}
			} else if end.CleanEOF || end.Err == nil || end.SourcePacketCount != 0 || result.Status != video.Incomplete || result.SourcePacketCount != 0 {
				t.Fatalf("damaged source: end %+v result %+v", end, result)
			}
			if tc.readErr == cause && !errors.Is(result.Err, cause) {
				t.Fatalf("lost source failure: %v", result.Err)
			}
		})
	}
}

type initialErrorFile struct {
	memFileInfo
	cause error
}

func (f *initialErrorFile) OpenRead() (io.ReadCloser, error) {
	return &initialErrorReader{file: f}, nil
}

type initialErrorReader struct {
	file *initialErrorFile
	pos  int
}

func (r *initialErrorReader) Read(b []byte) (int, error) {
	n := copy(b, r.file.data[r.pos:])
	r.pos += n
	if r.pos == len(r.file.data) {
		if n > 0 {
			return n, r.file.cause
		}
		return 0, io.EOF
	}
	return n, nil
}
func (*initialErrorReader) Close() error { return nil }

func TestVideoCollectionInitialReadError(t *testing.T) {
	for _, cause := range []error{errors.New("initial source failure"), io.EOF} {
		t.Run(cause.Error(), func(t *testing.T) {
			data := append([]byte{0, 0, 0, 0}, collectionPacket(0x1011, 0, true, collectionPES([]byte{1}, 0, 0, true))...)
			s := NewStreamFile(&initialErrorFile{memFileInfo: memFileInfo{name: "TEST.M2TS", data: data}, cause: cause})
			s.Streams[0x1011] = &stream.VideoStream{Stream: stream.Stream{PID: 0x1011, StreamType: stream.StreamTypeHEVCVideo}}
			r := &recordingVideo{}
			s.VideoConsumer = func(context.Context, video.StreamInfo) (video.Consumer, error) { return r, nil }
			if err := s.Scan(nil, false); err != nil {
				t.Fatal(err)
			}
			if len(r.ends) != 1 || !bytes.Equal(r.data, []byte{1}) {
				t.Fatalf("end %+v bytes %x", r.ends, r.data)
			}
			if cause == io.EOF {
				if !r.ends[0].CleanEOF {
					t.Fatal(r.ends)
				}
			} else if r.ends[0].CleanEOF || !errors.Is(s.Collection[0].Err, cause) {
				t.Fatalf("lost initial failure: %+v", s.Collection)
			}
		})
	}
}

func TestVideoCollectionOptionalHeaderBounds(t *testing.T) {
	tests := []struct {
		name   string
		packet []byte
		valid  bool
	}{}
	for _, flags := range []byte{0x20, 0x10, 0x08, 0x04, 0x02, 0x01} {
		pes := []byte{0, 0, 1, 0xe0, 0, 4, 0x80, flags, 0, 1}
		tests = append(tests, struct {
			name   string
			packet []byte
			valid  bool
		}{fmt.Sprintf("missing PES field %02x", flags), collectionPacket(0x1011, 0, true, pes), false})
	}
	for _, scramble := range []byte{0x10, 0x20, 0x30} {
		pes := []byte{0, 0, 1, 0xe0, 0, 4, 0x80 | scramble, 0, 0, 1}
		tests = append(tests, struct {
			name   string
			packet []byte
			valid  bool
		}{fmt.Sprintf("PES scrambling %02x", scramble), collectionPacket(0x1011, 0, true, pes), false})
	}
	for _, flags := range []byte{0x10, 0x08, 0x04, 0x02, 0x01} {
		p := collectionPacket(0x1011, 0, true, collectionPES(make([]byte, 163), 0, 0, true))
		p[5] = flags
		tests = append(tests, struct {
			name   string
			packet []byte
			valid  bool
		}{fmt.Sprintf("missing adaptation field %02x", flags), p, false})
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			data := append(tc.packet, collectionPacket(0x1fff, 0, false, []byte{0})...)
			r, res := scanVideo(t, data)
			if r.ends[0].CleanEOF != tc.valid || (res[0].Status == video.Complete) != tc.valid {
				t.Fatalf("end %+v result %+v", r.ends, res)
			}
		})
	}
}

func TestVideoCollectionDamagedAndAdjacentPSI(t *testing.T) {
	pat := psiWithCRC([]byte{0, 0xb0, 13, 0, 1, 0xc1, 0, 0, 0, 1, 0xf0, 0})
	pmt := psiWithCRC([]byte{2, 0xb0, 18, 0, 1, 0xc1, 0, 0, 0xf0, 0x11, 0xf0, 0, 0x24, 0xf0, 0x11, 0xf0, 0})
	replacement := psiWithCRC([]byte{2, 0xb0, 18, 0, 1, 0xc3, 0, 0, 0xf0, 0x12, 0xf0, 0, 0x24, 0xf0, 0x12, 0xf0, 0})
	prefix := collectionPacket(0, 0, true, append([]byte{0}, pat...))
	prefix = append(prefix, collectionPacket(0x1000, 0, true, append([]byte{0}, pmt...))...)
	prefix = append(prefix, collectionPacket(0x1011, 0, true, collectionPES([]byte{1}, 0, 0, true))...)
	for _, mode := range []string{"TEI", "scrambling", "reserved control", "adaptation length", "lost packet", "truncated section", "adjacent sections", "pointer completion"} {
		t.Run(mode, func(t *testing.T) {
			q := collectionPacket(0x1000, 1, true, append([]byte{0}, replacement...))
			var tail []byte
			switch mode {
			case "TEI":
				q[1] |= 0x80
			case "scrambling":
				q[3] |= 0x80
			case "reserved control":
				q[3] &= 0xcf
			case "adaptation length":
				q[4] = 184
			case "lost packet":
				q = collectionPacket(0x1000, 3, true, append([]byte{0}, pmt...))
			case "truncated section":
				q = collectionPacket(0x1000, 1, true, append([]byte{0}, replacement[:8]...))
			case "adjacent sections":
				q = collectionPacket(0x1000, 1, true, append(append([]byte{0}, pmt...), replacement...))
			case "pointer completion":
				q = collectionPacket(0x1000, 1, true, append([]byte{0}, replacement[:8]...))
				tail = collectionPacket(0x1000, 2, true, append(append([]byte{byte(len(replacement) - 8)}, replacement[8:]...), pmt...))
			}
			data := append(append(append([]byte(nil), prefix...), q...), tail...)
			r, res := scanVideo(t, data)
			if res[0].Status == video.Complete || r.ends[0].CleanEOF {
				t.Fatalf("damage hidden: %+v", res)
			}
		})
	}
}

func TestCollectionValidOptionalFields(t *testing.T) {
	pesCases := []struct {
		name   string
		flags  byte
		fields []byte
	}{
		{"ESCR", 0x20, []byte{4, 0, 4, 0, 4, 1}},
		{"ES rate", 0x10, []byte{0x80, 0, 1}},
		{"trick mode", 8, []byte{0}},
		{"copy info", 4, []byte{0x80}},
		{"PES CRC", 2, []byte{0, 0}},
		{"extension", 1, []byte{0x0e}},
		{"private data", 1, append([]byte{0x8e}, make([]byte, 16)...)},
		{"pack header", 1, []byte{0x4e, 2, 0, 0}},
		{"sequence counter", 1, []byte{0x2e, 0x80, 0x80}},
		{"P-STD", 1, []byte{0x1e, 0x40, 0}},
		{"extension2 stream ID", 1, []byte{0x0f, 0x81, 0}},
		{"stuffing", 0, []byte{0xff, 0xff}},
	}
	for _, tc := range pesCases {
		t.Run("PES/"+tc.name, func(t *testing.T) {
			pes := []byte{0, 0, 1, 0xe0, 0, byte(4 + len(tc.fields)), 0x80, tc.flags, byte(len(tc.fields))}
			pes = append(append(pes, tc.fields...), 1)
			r, res := scanVideo(t, append(collectionPacket(0x1011, 0, true, pes), collectionPacket(0x1fff, 0, false, []byte{0})...))
			if res[0].Status != video.Complete || !bytes.Equal(r.data, []byte{1}) || r.boundaries[0].HasPTS || r.boundaries[0].HasDTS {
				t.Fatalf("valid optional header rejected: %+v", res)
			}
			if tc.flags != 0 {
				short := append([]byte(nil), pes[:9]...)
				short[5]--
				short[8]--
				short = append(append(short, tc.fields[:len(tc.fields)-1]...), 1)
				_, bad := scanVideo(t, append(collectionPacket(0x1011, 0, true, short), collectionPacket(0x1fff, 0, false, []byte{0})...))
				if bad[0].Status == video.Complete {
					t.Fatal("optional field borrowed payload bytes")
				}
			}
		})
	}
	stamp := encodePTS(0x30, 0)
	adapCases := []struct {
		name   string
		flags  byte
		fields []byte
	}{
		{"PCR", 0x10, []byte{0, 0, 0, 0, 0x7e, 0}},
		{"OPCR", 0x18, []byte{0, 0, 0, 0, 0x7e, 0, 0, 0, 0, 0, 0x7e, 0}},
		{"splice countdown", 4, []byte{0}},
		{"private data", 2, []byte{2, 1, 2}},
		{"extension", 1, []byte{1, 0x1f}},
		{"LTW", 1, []byte{3, 0x9f, 0, 0}},
		{"piecewise rate", 1, []byte{4, 0x5f, 0xc0, 0, 0}},
		{"seamless splice", 5, append([]byte{0, 6, 0x3f}, stamp[:]...)},
		{"AF descriptor", 1, []byte{4, 0x0f, 2, 1, 0}},
	}
	for _, tc := range adapCases {
		t.Run("adaptation/"+tc.name, func(t *testing.T) {
			p := bytes.Repeat([]byte{0xff}, 188)
			copy(p, []byte{0x47, 0x50, 0x11, 0x30, byte(1 + len(tc.fields)), tc.flags})
			copy(p[6:], tc.fields)
			copy(p[6+len(tc.fields):], collectionPES([]byte{1}, 0, 0, true))
			r, res := scanVideo(t, append(p, collectionPacket(0x1fff, 0, false, []byte{0})...))
			if res[0].Status != video.Complete || !bytes.Equal(r.data, []byte{1}) {
				t.Fatalf("valid optional adaptation rejected: %+v", res)
			}
			p[4]--
			copy(p[5+int(p[4]):], collectionPES([]byte{1}, 0, 0, true))
			_, bad := scanVideo(t, append(p, collectionPacket(0x1fff, 0, false, []byte{0})...))
			if bad[0].Status == video.Complete {
				t.Fatal("adaptation field borrowed payload bytes")
			}
		})
	}
}

func TestCollectionOptionalMarkers(t *testing.T) {
	for _, tc := range []struct {
		name   string
		flags  byte
		fields []byte
	}{
		{"ESCR", 0x20, []byte{0, 0, 4, 0, 4, 1}},
		{"rate", 0x10, []byte{0, 0, 1}},
		{"copy", 4, []byte{0}},
		{"sequence", 1, []byte{0x2e, 0, 0x80}},
		{"P-STD", 1, []byte{0x1e, 0, 0}},
		{"extension2", 1, []byte{0x0f, 1, 0}},
		{"stuffing", 0, []byte{0}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			pes := []byte{0, 0, 1, 0xe0, 0, byte(4 + len(tc.fields)), 0x80, tc.flags, byte(len(tc.fields))}
			pes = append(append(pes, tc.fields...), 1)
			_, res := scanVideo(t, append(collectionPacket(0x1011, 0, true, pes), collectionPacket(0x1fff, 0, false, []byte{0})...))
			if res[0].Status == video.Complete {
				t.Fatal("invalid marker accepted")
			}
		})
	}
}

func TestCollectionPSIValidSplitsDuplicatesAndUnrelatedDamage(t *testing.T) {
	pat := psiWithCRC([]byte{0, 0xb0, 13, 0, 1, 0xc1, 0, 0, 0, 1, 0xf0, 0})
	pmt := psiWithCRC([]byte{2, 0xb0, 18, 0, 1, 0xc1, 0, 0, 0xf0, 0x11, 0xf0, 0, 0x24, 0xf0, 0x11, 0xf0, 0})
	for split := 1; split < len(pmt); split++ {
		t.Run(fmt.Sprint(split), func(t *testing.T) {
			data := collectionPacket(0, 0, true, append([]byte{0}, pat...))
			q := collectionPacket(0x1000, 15, true, append([]byte{0}, pmt[:split]...))
			data = append(append(data, q...), q...) // one legal duplicate, then CC wrap
			data = append(data, collectionPacket(0x1000, 0, true, append(append([]byte{byte(len(pmt) - split)}, pmt[split:]...), pmt...))...)
			bad := collectionPacket(0x100, 0, false, []byte{0})
			bad[1] |= 0x80
			data = append(data, bad...)
			data = append(data, collectionPacket(0x1011, 0, true, collectionPES([]byte{1}, 0, 0, true))...)
			_, res := scanVideo(t, data)
			if res[0].Status != video.Complete {
				t.Fatal(res)
			}
		})
	}
}
