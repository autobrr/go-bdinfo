package codec

import (
	"testing"

	"github.com/autobrr/go-bdinfo/internal/stream"
)

func TestDTSSpeakerActivityMaskChannelLayout(t *testing.T) {
	mask := uint16(0x0001 | 0x0002 | 0x0004 | 0x0008 | 0x0020)

	if got := dtsHDSpeakerActivityMaskChannelLayout(mask); got != "C L R Ls Rs LFE Lh Rh" {
		t.Fatalf("dtsHDSpeakerActivityMaskChannelLayout()=%q", got)
	}
}

func TestDTSSpeakerActivityMaskRearHeightPair(t *testing.T) {
	layout := dtsHDSpeakerActivityMaskChannelLayout(0x0002 | 0x0004 | 0x0008 | 0x8000)
	if layout != "L R Ls Rs LFE Lhr Rhr" {
		t.Fatalf("dtsHDSpeakerActivityMaskChannelLayout()=%q", layout)
	}

	a := stream.AudioStream{ChannelLayoutText: layout}
	if got := a.ChannelDescription(); got != "4.1.2" {
		t.Fatalf("ChannelDescription()=%q want 4.1.2", got)
	}
}

func TestDetectDTSXStaysInsideTransfer(t *testing.T) {
	xll := []byte{0x41, 0xA2, 0x95, 0x47}
	dtsx := []byte{0x02, 0x00, 0x08, 0x50}
	first := append(append([]byte{0, 0, 0, 0}, xll...), make([]byte, 16)...)
	second := append([]byte{0, 0}, dtsx...)
	data := append(append([]byte{}, first...), second...)
	ends := []int{len(first), len(data)}

	if detectDTSX(data[:transferEnd(0, ends, len(data))]) {
		t.Fatal("DTS:X pattern in the next transfer must not count")
	}
	if !detectDTSX(data[:transferEnd(0, nil, len(data))]) {
		t.Fatal("unbounded scan should still find the pattern")
	}
	if got := transferEnd(len(first), ends, len(data)); got != len(data) {
		t.Fatalf("transferEnd(second) = %d, want %d", got, len(data))
	}
}
