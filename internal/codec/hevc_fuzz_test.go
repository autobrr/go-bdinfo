package codec

import (
	"bytes"
	"testing"

	"github.com/autobrr/go-bdinfo/internal/settings"
	"github.com/autobrr/go-bdinfo/internal/stream"
)

func FuzzScanHEVC(f *testing.F) {
	f.Add([]byte{0x00, 0x00, 0x01, 0x42, 0x01, 0x01, 0x01})
	f.Add([]byte{0x00, 0x00, 0x01, 0x4E, 0x01, 0x9a, 0x00})
	cfg := settings.Default(".")

	f.Fuzz(func(t *testing.T, data []byte) {
		// SPS/SEI parsing only needs small Annex-B payloads; cap fuzz inputs to
		// avoid CI timeout flakes on pathological discovery churn.
		if len(data) > 64<<10 {
			return
		}
		if !bytes.Contains(data, []byte{0x00, 0x00, 0x01}) && !bytes.Contains(data, []byte{0x00, 0x00, 0x00, 0x01}) {
			return
		}
		v := &stream.VideoStream{}
		v.StreamType = stream.StreamTypeHEVCVideo
		ScanHEVC(v, data, cfg)
	})
}
