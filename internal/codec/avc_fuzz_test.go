package codec

import (
	"testing"

	"github.com/autobrr/go-bdinfo/internal/stream"
)

func FuzzScanAVC(f *testing.F) {
	f.Add([]byte{0x00, 0x00, 0x01, 0x67, 0x64, 0x00, 0x28})
	f.Add([]byte{0x00, 0x00, 0x01, 0x09, 0x10})

	f.Fuzz(func(t *testing.T, data []byte) {
		if len(data) > 2<<20 {
			return
		}
		v := &stream.VideoStream{}
		v.StreamType = stream.StreamTypeAVCVideo
		tag := ""
		ScanAVC(v, data, &tag)
	})
}
