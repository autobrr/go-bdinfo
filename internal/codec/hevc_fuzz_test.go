package codec

import (
	"testing"

	"github.com/autobrr/go-bdinfo/internal/settings"
	"github.com/autobrr/go-bdinfo/internal/stream"
)

func FuzzScanHEVC(f *testing.F) {
	f.Add([]byte{0x00, 0x00, 0x01, 0x42, 0x01, 0x01, 0x01})
	f.Add([]byte{0x00, 0x00, 0x01, 0x4E, 0x01, 0x9a, 0x00})

	f.Fuzz(func(t *testing.T, data []byte) {
		if len(data) > 2<<20 {
			return
		}
		v := &stream.VideoStream{}
		v.StreamType = stream.StreamTypeHEVCVideo
		ScanHEVC(v, data, settings.Default("."))
	})
}
