package codec

import (
	"testing"

	"github.com/autobrr/go-bdinfo/internal/stream"
)

func pgsPCS(objects ...byte) []byte {
	seg := []byte{0x07, 0x80, 0x04, 0x38, 0x10, 0x00, 0x01, 0x80, 0x00, 0x00, byte(len(objects))}
	for _, cropped := range objects {
		seg = append(seg, 0x00, 0x00, 0x00, cropped, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0)
	}
	return append([]byte{0x16, byte(len(seg) >> 8), byte(len(seg))}, seg...)
}

func TestScanPGS(t *testing.T) {
	ods := []byte{0x15, 0x00, 0x04, 0x00, 0x00, 0x00, 0x00}
	end := []byte{0x80, 0x00, 0x00}
	g := stream.NewGraphicsStream()
	for _, transfer := range [][]byte{
		pgsPCS(0x00), ods, end, // normal caption
		pgsPCS(0x40), ods, ods, end, // forced, ODS split in two fragments counts twice
		pgsPCS(), ods, // empty composition after END: not counted
	} {
		ScanPGS(g, transfer)
	}
	if got, want := g.Description(), "1920x1080 / 1 Caption (2 Forced Captions)"; got != want {
		t.Fatalf("description = %q, want %q", got, want)
	}
}
