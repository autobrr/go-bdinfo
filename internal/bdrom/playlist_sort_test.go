package bdrom

import (
	"testing"

	"github.com/autobrr/go-bdinfo/internal/stream"
)

func TestCompareGraphicsStreams_EnglishPIDAscendingWithinLanguage(t *testing.T) {
	a := stream.NewGraphicsStream()
	a.StreamType = stream.StreamTypePresentationGraphics
	a.PID = 2000
	a.SetLanguageCode("eng")

	b := stream.NewGraphicsStream()
	b.StreamType = stream.StreamTypePresentationGraphics
	b.PID = 3000
	b.SetLanguageCode("eng")

	if got := compareGraphicsStreams(a, b); got >= 0 {
		t.Fatalf("expected lower PID english stream first, got compare=%d", got)
	}
	if got := compareGraphicsStreams(b, a); got <= 0 {
		t.Fatalf("expected higher PID english stream after lower PID, got compare=%d", got)
	}
}

func TestCompareAudioStreams_EnglishPIDAscendingWithinLanguage(t *testing.T) {
	a := &stream.AudioStream{}
	a.StreamType = stream.StreamTypeAC3Audio
	a.ChannelCount = 2
	a.PID = 2000
	a.SetLanguageCode("eng")

	b := &stream.AudioStream{}
	b.StreamType = stream.StreamTypeAC3Audio
	b.ChannelCount = 2
	b.PID = 3000
	b.SetLanguageCode("eng")

	if got := compareAudioStreams(a, b); got >= 0 {
		t.Fatalf("expected lower PID english stream first, got compare=%d", got)
	}
	if got := compareAudioStreams(b, a); got <= 0 {
		t.Fatalf("expected higher PID english stream after lower PID, got compare=%d", got)
	}
}

func TestCompareTextStreams_EnglishPIDAscendingWithinLanguage(t *testing.T) {
	a := stream.NewTextStream()
	a.StreamType = stream.StreamTypeSubtitle
	a.PID = 2000
	a.SetLanguageCode("eng")

	b := stream.NewTextStream()
	b.StreamType = stream.StreamTypeSubtitle
	b.PID = 3000
	b.SetLanguageCode("eng")

	if got := compareTextStreams(a, b); got >= 0 {
		t.Fatalf("expected lower PID english stream first, got compare=%d", got)
	}
	if got := compareTextStreams(b, a); got <= 0 {
		t.Fatalf("expected higher PID english stream after lower PID, got compare=%d", got)
	}
}
