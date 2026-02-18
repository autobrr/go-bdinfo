package codec

import "testing"

func TestHEVCFrameTagFromTransfer_InitializedVsUninitialized(t *testing.T) {
	// Build an Annex-B transfer with two slice NAL units:
	// 1) first slice of picture => tag "I"
	// 2) non-first slice => tag "" (null)
	//
	// BDInfo behavior:
	// - when initialized: stop at first non-null tag ("I")
	// - when uninitialized: keep scanning; last slice wins ("" here)
	start := []byte{0x00, 0x00, 0x01}

	// NAL header bytes: forbidden_zero_bit=0, nal_unit_type in bits[6..1], temporal_id_plus1 != 0.
	nalHeader := func(nalUnitType byte) []byte {
		return []byte{nalUnitType << 1, 0x01}
	}

	// Slice RBSP bits (MSB-first):
	// first_slice_segment_in_pic_flag(1)
	// slice_pic_parameter_set_id ue(v) (0 => "1")
	// slice_type ue(v) (2 => bits "0 1 1")
	sliceFirstI := []byte{0xD8} // 11011000

	// Non-first slice:
	// first_slice_segment_in_pic_flag(0)
	// slice_pic_parameter_set_id ue(v) (0 => "1")
	sliceNotFirst := []byte{0x40} // 01000000

	transfer := make([]byte, 0, 64)
	transfer = append(transfer, start...)
	transfer = append(transfer, nalHeader(1)...)
	transfer = append(transfer, sliceFirstI...)
	transfer = append(transfer, start...)
	transfer = append(transfer, nalHeader(1)...)
	transfer = append(transfer, sliceNotFirst...)

	var st HEVCTagState
	st.ppsValid[0] = true
	st.pps[0] = hevcPPS{
		dependentSliceSegmentsEnabled: false,
		numExtraSliceHeaderBits:       0,
	}

	if got := HEVCFrameTagFromTransfer(&st, transfer, true); got != "I" {
		t.Fatalf("initialized: got %q, want %q", got, "I")
	}
	if got := HEVCFrameTagFromTransfer(&st, transfer, false); got != "" {
		t.Fatalf("uninitialized: got %q, want empty", got)
	}
}
