package bdrom

import (
	"testing"

	"github.com/autobrr/go-bdinfo/internal/stream"
)

// This is an end-to-end integration test for the HEVC per-transfer tag path: it
// scans a synthetic Annex-B HEVC transport stream and checks the StreamDiagnostics
// tags. The codec scanner itself is proven byte-identical to the batch oracle by
// FuzzHEVCTagScannerEquivalence; this test exercises the streamfile.go wiring around
// it — the uninitialized->initialized transition, scan-while-buffering with the
// per-transfer reset, and HEVCTagState persistence across transfers.

// Minimal HEVC NAL bodies that the tag parser accepts (see internal/codec).
var (
	hevcStartCode = []byte{0x00, 0x00, 0x01}
	// SPS (nal type 33): vps_id/maxSubLayers=0/nesting=0, zero profile_tier_level
	// (12 bytes), then sps_id ue(0). Parses to spsValid[0]=true.
	hevcSPS = append([]byte{0x42, 0x01, 0x00},
		append(make([]byte, 12), 0x80)...)
	// PPS (nal type 34): pps_id ue(0), sps_id ue(0), dependent=0, output=0, extra=0.
	hevcPPS = []byte{0x44, 0x01, 0xC0}
	// Slice (nal type 1) headers: first_slice=1, pps_id=0, slice_type {0:P,1:B,2:I}.
	hevcSliceI = []byte{0x02, 0x01, 0xD8}
	hevcSliceP = []byte{0x02, 0x01, 0xE0}
	hevcSliceB = []byte{0x02, 0x01, 0xD0}
)

func hevcNAL(nal []byte) []byte { return append(append([]byte{}, hevcStartCode...), nal...) }

// hevcSEI builds a non-VCL SEI NAL (type 39) with a body of n start-code-free bytes,
// so a transfer can be made large enough to span multiple TS packets (forcing the
// streaming scanner to feed incrementally before the trailing slice resolves).
func hevcSEI(n int) []byte {
	nal := []byte{39 << 1, 0x01}
	body := make([]byte, n)
	for i := range body {
		body[i] = byte(i*7+1) | 0x04 // avoid 00 00 0x sequences
	}
	return append(nal, body...)
}

// hevcFrameES builds the elementary-stream (Annex-B) payload for one access unit.
func hevcFrameES(nals ...[]byte) []byte {
	var es []byte
	for _, n := range nals {
		es = append(es, hevcNAL(n)...)
	}
	return es
}

// hevcPESPackets wraps a frame's ES in a PES header (PTS+DTS) and splits it across
// 188-byte TS packets on the given PID, so multi-packet transfers are exercised.
func hevcPESPackets(pid uint16, pts, dts uint64, es []byte) []byte {
	ptsB := encodePTS(0x30, pts)
	dtsB := encodePTS(0x10, dts)
	pes := []byte{0x00, 0x00, 0x01, 0xE0, 0x00, 0x00, 0x80, 0xC0, 0x0A}
	pes = append(pes, ptsB[:]...)
	pes = append(pes, dtsB[:]...)
	pes = append(pes, es...)

	var out []byte
	first := true
	for len(pes) > 0 {
		take := min(len(pes), 184)
		var payload [184]byte
		copy(payload[:], pes[:take])
		pkt := tsPacket188(pid, first, payload[:])
		out = append(out, pkt[:]...)
		pes = pes[take:]
		first = false
	}
	return out
}

func TestStreamFileHEVCTagScan_Integration(t *testing.T) {
	const pid = 0x1011

	// Frame 1 carries SPS+PPS (so the scan transitions to initialized after it) plus
	// an I slice. Frames 2..5 carry only a slice each — proving HEVCTagState (the PPS)
	// persists across transfers and the streaming scanner resolves single-slice frames.
	frames := []struct {
		slice []byte
		tag   string
		extra [][]byte // NALs before the slice
	}{
		{slice: hevcSliceI, tag: "I", extra: [][]byte{hevcSPS, hevcPPS}},
		{slice: hevcSliceP, tag: "P"},
		// A ~600-byte SEI before the slice makes this transfer span several TS packets,
		// so the streaming scanner feeds incrementally and resolves the trailing slice
		// only once enough packets have arrived.
		{slice: hevcSliceB, tag: "B", extra: [][]byte{hevcSEI(600)}},
		{slice: hevcSliceI, tag: "I"},
		{slice: hevcSliceP, tag: "P"},
	}

	var data []byte
	for i, f := range frames {
		nals := append(append([][]byte{}, f.extra...), f.slice)
		dts := uint64(90000 * (i + 1))
		data = append(data, hevcPESPackets(pid, dts, dts, hevcFrameES(nals...))...)
	}

	fi := &memFileInfo{name: "HEVC.M2TS", data: data}
	s := NewStreamFile(fi)
	s.Streams[pid] = &stream.VideoStream{Stream: stream.Stream{PID: pid, StreamType: stream.StreamTypeHEVCVideo}}

	// A non-nil playlist is required for updateStreamBitrates (and thus the
	// StreamDiagnostics rows) to run; no matching clips are needed for the tag path.
	playlists := []*PlaylistFile{{}}
	if err := s.ScanWithProgress(playlists, false, nil); err != nil {
		t.Fatalf("Scan() error: %v", err)
	}

	var gotTags []string
	for _, d := range s.StreamDiagnostics[pid] {
		gotTags = append(gotTags, d.Tag)
	}

	// The tag recorded at frame K's DTS flush is the tag of transfer K-1 (resolved at
	// frame K's PES start). With 5 frames the flushes at frames 2..5 record frames 1..4:
	// I (frame1, uninitialized batch), then P, B, I (frames 2..4, streaming scanner).
	want := []string{"I", "P", "B", "I"}
	if len(gotTags) != len(want) {
		t.Fatalf("got %d diagnostics tags %q, want %d %q", len(gotTags), gotTags, len(want), want)
	}
	for i := range want {
		if gotTags[i] != want[i] {
			t.Fatalf("diagnostics tag[%d]=%q, want %q (all: %q)", i, gotTags[i], want[i], gotTags)
		}
	}
}
