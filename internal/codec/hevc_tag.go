package codec

import (
	"bytes"

	"github.com/autobrr/go-bdinfo/internal/buffer"
)

// startCode3 is the 3-byte Annex-B start-code prefix (00 00 01) shared by both
// the 3-byte and 4-byte (00 00 00 01) start-code forms.
var startCode3 = []byte{0x00, 0x00, 0x01}

// nextStartCode finds the next Annex-B start code at or after start within data
// and returns its index and length (3 or 4), or (-1, 0) when none is found.
//
// It is byte-for-byte equivalent to the original linear scan
//
//	for i := start; i+3 < len(data); i++ {
//		if data[i] != 0 || data[i+1] != 0 { continue }
//		if data[i+2] == 1 { return i, 3 }
//		if i+3 < len(data) && data[i+2] == 0 && data[i+3] == 1 { return i, 4 }
//	}
//
// but uses bytes.Index for a vectorized search over the (often multi-KB) NAL
// bodies between start codes, which dominated scan CPU in profiling. Equivalence
// is enforced by FuzzNextStartCodeEquivalence.
func nextStartCode(data []byte, start int) (int, int) {
	if start < 0 {
		start = 0
	}
	rel := bytes.Index(data[start:], startCode3)
	if rel < 0 {
		return -1, 0
	}
	p := start + rel
	// 4-byte form 00 00 00 01: the 00 00 01 match at p is preceded by an extra
	// 0x00. Its bound (p-1)+3 < len is always satisfied because the match exists
	// at p (so p+2 < len). The p-1 >= start guard mirrors the original loop,
	// which started at i=start and so reported a 3-byte code when the leading
	// extra zero fell before start.
	if p-1 >= start && data[p-1] == 0x00 {
		return p - 1, 4
	}
	// 3-byte form requires p+3 < len(data) to match the original loop bound,
	// which stopped at i+3 < len and never reported a start code in the final
	// three bytes. bytes.Index already returns the earliest match, so if this
	// one is out of range no earlier valid start code exists.
	if p+3 < len(data) {
		return p, 3
	}
	return -1, 0
}

// HEVCTagState holds the minimal HEVC state needed to derive BDInfo-compatible
// per-transfer frame tags (I/P/B) for chapter diagnostics.
type HEVCTagState struct {
	spsValid [64]bool
	pps      [64]hevcPPS
	ppsValid [64]bool
}

func (s *HEVCTagState) HasSPS() bool {
	if s == nil {
		return false
	}
	for _, v := range s.spsValid {
		if v {
			return true
		}
	}
	return false
}

type hevcPPS struct {
	dependentSliceSegmentsEnabled bool
	numExtraSliceHeaderBits       uint8
}

// HEVCFrameTagFromTransfer scans a payload transfer for a slice header and returns
// "I", "P", or "B" when found. Empty string means "no tag" (null in official BDInfo).
//
// This is a minimal port of BDInfo TSCodecHEVC.Scan + SliceSegmentLayer logic:
// - parse PPS (NAL type 34) for dependent_slice_segments_enabled_flag and num_extra_slice_header_bits
// - parse slice header for first_slice_segment_in_pic_flag and slice_type
func HEVCFrameTagFromTransfer(state *HEVCTagState, data []byte, isInitialized bool) string {
	if state == nil || len(data) < 6 {
		return ""
	}

	tag := ""

	pos, scLen := nextStartCode(data, 0)
	for pos != -1 {
		nalStart := pos + scLen
		nextPos, nextLen := nextStartCode(data, nalStart)
		nalEnd := len(data)
		if nextPos != -1 {
			nalEnd = nextPos
		}

		nal := data[nalStart:nalEnd]
		if len(nal) >= 3 {
			// Basic header validation: forbidden_zero_bit must be 0 and nuh_temporal_id_plus1 must be non-zero.
			if (nal[0]&0x80) == 0 && (nal[1]&0x07) != 0 {
				nalUnitType := (nal[0] >> 1) & 0x3F
				switch nalUnitType {
				case 33: // SPS
					parseHEVCSPS(state, nal)
				case 34: // PPS
					parseHEVCPPS(state, nal)
				default:
					if t := parseHEVCSliceTag(state, nal, nalUnitType); isInitialized {
						// Match BDInfo: once initialized, stop at the first non-null slice tag.
						if t != "" {
							return t
						}
					} else {
						// Match BDInfo: while not initialized, keep scanning and let the last slice win
						// (can overwrite a prior tag with null).
						tag = t
					}
				}
			}
		}

		pos, scLen = nextPos, nextLen
	}
	return tag
}

func parseHEVCSPS(state *HEVCTagState, nal []byte) {
	// NAL header is 2 bytes.
	rbsp := RemoveEmulationBytes(nal[2:])
	br := buffer.NewBitReader(rbsp)

	// sps_video_parameter_set_id: u(4)
	if !br.SkipBits(4) {
		return
	}
	// sps_max_sub_layers_minus1: u(3)
	maxSub, ok := br.ReadBits(3)
	if !ok {
		return
	}
	// sps_temporal_id_nesting_flag: u(1)
	if !br.SkipBits(1) {
		return
	}

	if !skipHEVCProfileTierLevel(br, int(maxSub)) {
		return
	}

	// sps_seq_parameter_set_id: ue(v)
	spsID, ok := br.ReadUE()
	if !ok || spsID >= 64 {
		return
	}
	state.spsValid[spsID] = true
}

func skipHEVCProfileTierLevel(br *buffer.BitReader, maxSubLayersMinus1 int) bool {
	// general_profile_space(2) + general_tier_flag(1) + general_profile_idc(5)
	// general_profile_compatibility_flag[32]
	// general_constraint_indicator_flags[48]
	// general_level_idc(8)
	if !br.SkipBits(2 + 1 + 5 + 32 + 48 + 8) {
		return false
	}

	if maxSubLayersMinus1 < 0 {
		return false
	}
	if maxSubLayersMinus1 > 7 {
		maxSubLayersMinus1 = 7
	}

	subLayerProfilePresent := make([]bool, maxSubLayersMinus1)
	subLayerLevelPresent := make([]bool, maxSubLayersMinus1)
	for i := 0; i < maxSubLayersMinus1; i++ {
		b, ok := br.ReadBit()
		if !ok {
			return false
		}
		subLayerProfilePresent[i] = b == 1
		b, ok = br.ReadBit()
		if !ok {
			return false
		}
		subLayerLevelPresent[i] = b == 1
	}

	if maxSubLayersMinus1 > 0 {
		// reserved_zero_2bits for i = maxSubLayersMinus1 .. 7
		if !br.SkipBits((8 - maxSubLayersMinus1) * 2) {
			return false
		}
	}

	for i := 0; i < maxSubLayersMinus1; i++ {
		if subLayerProfilePresent[i] {
			// sub_layer_profile_space(2) + sub_layer_tier_flag(1) + sub_layer_profile_idc(5)
			// sub_layer_profile_compatibility_flag[32]
			// sub_layer_constraint_indicator_flags[48]
			if !br.SkipBits(2 + 1 + 5 + 32 + 48) {
				return false
			}
		}
		if subLayerLevelPresent[i] {
			// sub_layer_level_idc(8)
			if !br.SkipBits(8) {
				return false
			}
		}
	}
	return true
}

func parseHEVCPPS(state *HEVCTagState, nal []byte) {
	// NAL header is 2 bytes.
	rbsp := RemoveEmulationBytes(nal[2:])
	br := buffer.NewBitReader(rbsp)

	ppsID, ok := br.ReadUE()
	if !ok || ppsID >= 64 {
		return
	}

	spsID, ok := br.ReadUE()
	// Match BDInfo: ignore PPS when it references an out-of-range/unknown SPS id.
	if !ok || spsID >= 16 || !state.spsValid[spsID] {
		return
	}

	dependentBit, ok := br.ReadBit()
	if !ok {
		return
	}
	// output_flag_present_flag (skip 1)
	if !br.SkipBits(1) {
		return
	}
	extra, ok := br.ReadBits(3)
	if !ok {
		return
	}

	state.pps[ppsID] = hevcPPS{
		dependentSliceSegmentsEnabled: dependentBit == 1,
		numExtraSliceHeaderBits:       uint8(extra),
	}
	state.ppsValid[ppsID] = true
}

func parseHEVCSliceTag(state *HEVCTagState, nal []byte, nalUnitType byte) string {
	// Slice NAL unit types from BDInfo: 0-9, 16-21 (and only these are tagged).
	isSlice := (nalUnitType <= 9) || (nalUnitType >= 16 && nalUnitType <= 21)
	if !isSlice {
		return ""
	}

	// Match BDInfo TSStreamBuffer behavior: slice header mixes emulation skipping
	// (first flags read without skipping; Exp-Golomb reads with skipping).
	br := newHEVCTagBitReader(nal[2:])

	firstBit, ok := br.ReadBit(false)
	if !ok {
		return ""
	}
	firstSlice := firstBit == 1

	if nalUnitType >= 16 && nalUnitType <= 23 {
		// no_output_of_prior_pics_flag
		if _, ok := br.ReadBit(false); !ok {
			return ""
		}
	}

	ppsID, ok := br.ReadUE(true)
	if !ok || ppsID >= 64 || !state.ppsValid[ppsID] {
		return ""
	}
	pps := state.pps[ppsID]

	if !firstSlice {
		// Dependent slice segment flag only present when PPS enables it.
		if pps.dependentSliceSegmentsEnabled {
			_, _ = br.ReadBit(true)
		}
		return ""
	}

	if pps.numExtraSliceHeaderBits > 0 {
		if !br.SkipBits(int(pps.numExtraSliceHeaderBits), true) {
			return ""
		}
	}
	sliceType, ok := br.ReadUE(true)
	if !ok {
		return ""
	}
	switch sliceType {
	case 0:
		return "P"
	case 1:
		return "B"
	case 2:
		return "I"
	default:
		return ""
	}
}

// hevcTagBitReader emulates the specific bit + emulation skipping semantics of
// BDInfo's TSStreamBuffer for HEVC slice header parsing.
type hevcTagBitReader struct {
	data     []byte
	pos      int
	skipBits int
}

func newHEVCTagBitReader(data []byte) *hevcTagBitReader {
	return &hevcTagBitReader{data: data}
}

func (r *hevcTagBitReader) ReadBit(skipEmulation bool) (uint64, bool) {
	if r.pos >= len(r.data) {
		return 0, false
	}

	startPos := r.pos
	tempByte := r.data[startPos]
	skippedBytes := 0

	if skipEmulation && tempByte == 0x03 && startPos >= 2 && r.data[startPos-2] == 0x00 && r.data[startPos-1] == 0x00 {
		// Skip H.26x emulation prevention byte (00 00 03).
		if startPos+1 >= len(r.data) {
			return 0, false
		}
		tempByte = r.data[startPos+1]
		skippedBytes = 1
	}

	bit := (tempByte >> (7 - uint8(r.skipBits))) & 0x01

	r.skipBits++
	r.pos = startPos + (r.skipBits >> 3) + skippedBytes
	r.skipBits &= 7

	return uint64(bit), true
}

func (r *hevcTagBitReader) ReadBits(n int, skipEmulation bool) (uint64, bool) {
	if n <= 0 {
		return 0, true
	}
	var v uint64
	for range n {
		b, ok := r.ReadBit(skipEmulation)
		if !ok {
			return 0, false
		}
		v = (v << 1) | b
	}
	return v, true
}

func (r *hevcTagBitReader) SkipBits(n int, skipEmulation bool) bool {
	_, ok := r.ReadBits(n, skipEmulation)
	return ok
}

func (r *hevcTagBitReader) ReadUE(skipEmulation bool) (uint64, bool) {
	zeros := 0
	for {
		b, ok := r.ReadBit(skipEmulation)
		if !ok {
			return 0, false
		}
		if b == 0 {
			zeros++
			if zeros > 63 {
				return 0, false
			}
			continue
		}
		break
	}
	if zeros == 0 {
		return 0, true
	}
	v, ok := r.ReadBits(zeros, skipEmulation)
	if !ok {
		return 0, false
	}
	return (1<<zeros - 1) + v, true
}
