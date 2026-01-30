package codec

import "github.com/autobrr/go-bdinfo/internal/stream"

func ScanMPEG2(v *stream.VideoStream, _ []byte) {
	if v.IsInitialized {
		return
	}
	v.IsVBR = true
	v.IsInitialized = true
}
