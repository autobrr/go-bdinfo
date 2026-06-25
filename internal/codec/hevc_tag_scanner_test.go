package codec

import (
	"testing"
)

// feedScanner drives a HEVCTagScanner over data split into chunks of the given
// sizes (simulating per-TS-packet arrival into the accumulated transfer buffer),
// finalizing on the last chunk. It returns the resolved tag. The scanner always
// sees a growing prefix of the same contiguous buffer, exactly as in streamfile.go.
func feedScanner(state *HEVCTagState, data []byte, step func(seed *uint32) int) string {
	var sc HEVCTagScanner
	tag := ""
	seed := uint32(0x9e3779b9)
	i := 0
	for i < len(data) {
		n := max(step(&seed), 1)
		j := min(i+n, len(data))
		tag, _ = sc.Scan(state, data[:j], j == len(data))
		i = j
	}
	if len(data) == 0 {
		tag, _ = sc.Scan(state, data, true)
	}
	return tag
}

// TestHEVCTagScanner_MatchesBatch_AcrossSplits checks the streaming scanner against
// the batch oracle on the same two-slice transfer used by the batch unit test, for
// every fixed chunk size from 1 byte (every start code straddles a boundary) up to
// the whole buffer at once.
func TestHEVCTagScanner_MatchesBatch_AcrossSplits(t *testing.T) {
	start := []byte{0x00, 0x00, 0x01}
	nalHeader := func(nalUnitType byte) []byte { return []byte{nalUnitType << 1, 0x01} }
	sliceFirstI := []byte{0xD8}   // first_slice=1, pps_id=0, slice_type=2 (I)
	sliceNotFirst := []byte{0x40} // first_slice=0

	transfer := make([]byte, 0, 64)
	transfer = append(transfer, start...)
	transfer = append(transfer, nalHeader(1)...)
	transfer = append(transfer, sliceFirstI...)
	transfer = append(transfer, start...)
	transfer = append(transfer, nalHeader(1)...)
	transfer = append(transfer, sliceNotFirst...)

	seedState := func() HEVCTagState {
		var st HEVCTagState
		st.ppsValid[0] = true
		st.pps[0] = hevcPPS{}
		return st
	}

	batchState := seedState()
	want := HEVCFrameTagFromTransfer(&batchState, transfer, true)
	if want != "I" {
		t.Fatalf("oracle sanity: got %q want I", want)
	}

	for chunk := 1; chunk <= len(transfer)+1; chunk++ {
		st := seedState()
		got := feedScanner(&st, transfer, func(*uint32) int { return chunk })
		if got != want {
			t.Fatalf("chunk=%d: scanner tag %q != batch %q", chunk, got, want)
		}
		if st != batchState {
			t.Fatalf("chunk=%d: scanner state diverged from batch", chunk)
		}
	}
}

// scNAL prepends a 3-byte start code to a NAL body.
func scNAL(nal ...byte) []byte { return append([]byte{0x00, 0x00, 0x01}, nal...) }

// hevcSPSBytes / hevcPPSBytes / hevcSlice are minimal NAL bodies the real parsers
// accept, used to build SPS->PPS->slice transfers that resolve to a non-empty tag.
var (
	// SPS (type 33): zero profile_tier_level (12 bytes) + sps_id ue(0) => spsValid[0]=true.
	hevcSPSBytes = append([]byte{0x42, 0x01, 0x00}, append(make([]byte, 12), 0x80)...)
	// PPS (type 34) body 0xE6 => pps_id 0, sps_id 0, dependent=1, output=0, extra=3.
	hevcPPSBytes = []byte{0x44, 0x01, 0xE6}
)

// TestHEVCTagScanner_BoundaryStraddle is a regression guard for the peek-past-NAL bug:
// a slice NAL whose slice_type Exp-Golomb code is truncated at the true NAL boundary
// must yield the SAME tag as the batch oracle, even when the next start code's leading
// 0x00 bytes follow it in the buffer. Before the fix, the early-resolve peek read those
// out-of-NAL bytes and latched "B" while the batch path (which delimits the NAL at the
// start code) yields "". The scanner must match the batch for every chunk split.
func TestHEVCTagScanner_BoundaryStraddle(t *testing.T) {
	// SPS, then PPS(extra=3) so slice_type sits just past the 1-byte slice body, then an
	// IRAP slice (type 19, body 0xA1) whose slice_type ue needs a bit beyond the NAL,
	// then a trailing non-first slice. Batch delimits the IRAP slice to [26 01 A1] -> "".
	transfer := scNAL(hevcSPSBytes...)
	transfer = append(transfer, scNAL(hevcPPSBytes...)...)
	transfer = append(transfer, scNAL(0x26, 0x01, 0xA1)...)
	transfer = append(transfer, scNAL(0x02, 0x01, 0x40)...)

	var batchState HEVCTagState
	want := HEVCFrameTagFromTransfer(&batchState, transfer, true)
	if want != "" {
		t.Fatalf("oracle sanity: a slice truncated at the NAL boundary must be null, got %q", want)
	}

	for chunk := 1; chunk <= len(transfer)+1; chunk++ {
		var st HEVCTagState
		got := feedScanner(&st, transfer, func(*uint32) int { return chunk })
		if got != want {
			t.Fatalf("chunk=%d: scanner tag %q != batch %q (peek read past the NAL boundary)", chunk, got, want)
		}
		if st != batchState {
			t.Fatalf("chunk=%d: scanner state diverged from batch", chunk)
		}
	}
}

// FuzzHEVCTagScannerEquivalence is the correctness guarantee that keeps rendered
// reports byte-identical: for arbitrary Annex-B input fed in arbitrary chunk splits,
// the streaming scanner must produce the same tag AND the same final HEVCTagState as
// the batch HEVCFrameTagFromTransfer(state, data, true) it replaces.
func FuzzHEVCTagScannerEquivalence(f *testing.F) {
	seeds := [][]byte{
		nil,
		{},
		{0x00, 0x00, 0x01},
		{0x00, 0x00, 0x01, 0x26, 0x01, 0x9a, 0x00},
		{0x00, 0x00, 0x01, 0x42, 0x01, 0x01, 0x01}, // SPS-ish (type 33)
		{0x00, 0x00, 0x01, 0x44, 0x01, 0x01, 0x01}, // PPS-ish (type 34)
		// AUD + sizeable SEI body + first VCL slice, like a UHD/DV access unit.
		buildFramePrefix(256),
		buildFramePrefix(2048),
		// Two slices: first 'I', then non-first (null) — exercises early stop.
		{0x00, 0x00, 0x01, 0x02, 0x01, 0xD8, 0x00, 0x00, 0x01, 0x02, 0x01, 0x40},
		// 4-byte start codes interleaved with 3-byte.
		{0x00, 0x00, 0x00, 0x01, 0x42, 0x01, 0xAA, 0x00, 0x00, 0x01, 0x02, 0x01, 0xD8},
		// SPS -> PPS -> I slice with body so the resolved-tag ("I") path is exercised
		// from a fresh state (parseHEVCPPS only validates a PPS whose SPS is valid, so
		// the chain must include a real SPS first).
		append(append(scNAL(hevcSPSBytes...), scNAL(hevcPPSBytes...)...),
			scNAL(0x26, 0x01, 0xD8, 0x00)...),
		// The boundary-straddle regression case (batch -> "", scanner must match).
		append(append(scNAL(hevcSPSBytes...), scNAL(hevcPPSBytes...)...),
			append(scNAL(0x26, 0x01, 0xA1), scNAL(0x02, 0x01, 0x40)...)...),
	}
	for _, s := range seeds {
		for _, seed := range []uint32{1, 2, 3, 7, 0x1234} {
			f.Add(s, seed)
		}
	}

	f.Fuzz(func(t *testing.T, data []byte, splitSeed uint32) {
		if len(data) > 256<<10 {
			return
		}

		var batchState HEVCTagState
		wantTag := HEVCFrameTagFromTransfer(&batchState, data, true)

		var streamState HEVCTagState
		s := splitSeed | 1 // seed the chunk PRNG; |1 keeps it off the zero fixpoint
		got := feedScanner(&streamState, data, func(*uint32) int {
			s = s*1664525 + 1013904223
			return int((s>>16)%7) + 1 // 1..7 byte chunks
		})

		if got != wantTag {
			t.Fatalf("tag mismatch: got %q want %q data=%x split=%d", got, wantTag, data, splitSeed)
		}
		if streamState != batchState {
			t.Fatalf("state mismatch data=%x split=%d", data, splitSeed)
		}
	})
}

// FuzzNextStartCodeResumeEquivalence pins the helper: searching from the canonical
// start (searchStart == canonStart) must be identical to nextStartCode, which is
// itself differentially tested against the original linear scan.
func FuzzNextStartCodeResumeEquivalence(f *testing.F) {
	seeds := [][]byte{
		nil,
		{0x00, 0x00, 0x01},
		{0x00, 0x00, 0x00, 0x01},
		{0x00, 0x00, 0x00, 0x00, 0x01},
		{0xAA, 0x00, 0x00, 0x01, 0xBB},
		{0x00, 0x00, 0x01, 0x00, 0x00, 0x01},
		{0x00, 0x00, 0x01, 0x00, 0x00},
	}
	for _, s := range seeds {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, data []byte) {
		for start := 0; start <= len(data); start++ {
			gotP, gotL := nextStartCodeResume(data, start, start)
			wantP, wantL := nextStartCode(data, start)
			if gotP != wantP || gotL != wantL {
				t.Fatalf("resume(s,s) != nextStartCode start=%d data=%x: got (%d,%d) want (%d,%d)",
					start, data, gotP, gotL, wantP, wantL)
			}
		}
	})
}
