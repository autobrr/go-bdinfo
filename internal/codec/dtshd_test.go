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
