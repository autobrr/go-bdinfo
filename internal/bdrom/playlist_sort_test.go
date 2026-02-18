package bdrom

import (
	"testing"

	"github.com/autobrr/go-bdinfo/internal/stream"
)

func TestCompareGraphicsStreams_EnglishPIDAscending(t *testing.T) {
	a := stream.NewGraphicsStream()
	a.StreamType = stream.StreamTypePresentationGraphics
	a.PID = 2000
	a.SetLanguageCode("eng")

	b := stream.NewGraphicsStream()
	b.StreamType = stream.StreamTypePresentationGraphics
	b.PID = 3000
	b.SetLanguageCode("eng")

	if got := compareGraphicsStreams(a, b); got >= 0 {
		t.Fatalf("expected a before b (ascending PID), got compare=%d", got)
	}
	if got := compareGraphicsStreams(b, a); got <= 0 {
		t.Fatalf("expected b after a (ascending PID), got compare=%d", got)
	}
}
