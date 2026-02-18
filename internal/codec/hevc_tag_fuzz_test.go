package codec

import "testing"

func FuzzHEVCFrameTagFromTransfer(f *testing.F) {
	f.Add([]byte{0x00, 0x00, 0x01, 0x26, 0x01, 0x9a, 0x00})
	f.Add([]byte{0x00, 0x00, 0x01, 0x42, 0x01, 0x01, 0x01}) // SPS-ish
	f.Add([]byte{0x00, 0x00, 0x01, 0x44, 0x01, 0x01, 0x01}) // PPS-ish

	f.Fuzz(func(t *testing.T, data []byte) {
		if len(data) > 2<<20 {
			return
		}
		state := &HEVCTagState{}
		_ = HEVCFrameTagFromTransfer(state, data, false)
		_ = HEVCFrameTagFromTransfer(state, data, true)
	})
}
