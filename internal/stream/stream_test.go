package stream

import "testing"

func TestAudioStreamChannelDescription_DolbyHeightLayout(t *testing.T) {
	a := &AudioStream{
		ChannelCount:      7,
		LFE:               1,
		ChannelLayoutText: "L R C LFE Ls Rs Tfl Tfr",
	}

	if got := a.ChannelDescription(); got != "5.1.2" {
		t.Fatalf("ChannelDescription()=%q want 5.1.2", got)
	}
}

func TestAudioStreamChannelDescription_DTSHeightLayout(t *testing.T) {
	a := &AudioStream{
		ChannelCount:      7,
		LFE:               1,
		ChannelLayoutText: "L R C LFE Ls Rs Lh Rh",
	}

	if got := a.ChannelDescription(); got != "5.1.2" {
		t.Fatalf("ChannelDescription()=%q want 5.1.2", got)
	}
}

func TestAudioStreamChannelDescription_NonHeightLayoutUsesLegacyCount(t *testing.T) {
	a := &AudioStream{
		ChannelCount:      7,
		LFE:               1,
		ChannelLayoutText: "L R C LFE Ls Rs Lss Rss",
	}

	if got := a.ChannelDescription(); got != "7.1" {
		t.Fatalf("ChannelDescription()=%q want 7.1", got)
	}
}
