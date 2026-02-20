package codec

import (
	"fmt"

	"github.com/autobrr/go-bdinfo/internal/stream"
)

func ScanVC1(v *stream.VideoStream, data []byte) {
	if v.IsInitialized {
		return
	}

	var parse uint32
	var sequenceHeaderParse byte
	isInterlaced := false

	for i := 0; i < len(data); i++ {
		parse = (parse << 8) | uint32(data[i])

		if parse == 0x0000010F {
			sequenceHeaderParse = 6
			continue
		}
		if sequenceHeaderParse > 0 {
			sequenceHeaderParse--
			switch sequenceHeaderParse {
			case 5:
				profileLevel := (parse & 0x38) >> 3
				if (parse&0xC0)>>6 == 3 {
					v.EncodingProfile = fmt.Sprintf("Advanced Profile %d", profileLevel)
				} else {
					v.EncodingProfile = fmt.Sprintf("Main Profile %d", profileLevel)
				}
			case 0:
				isInterlaced = (parse&0x40)>>6 > 0
			}
			v.IsVBR = true
			v.IsInitialized = true
			v.IsInterlaced = isInterlaced
		}
	}
}
