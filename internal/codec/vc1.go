// Copyright (c) 2026, s0up and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package codec

import (
	"fmt"

	"github.com/autobrr/go-bdinfo/internal/stream"
)

// ScanVC1 reads only the profile from the sequence header. TSCodecVC1.cs also
// reads the interlace bit, but keeps it local to decode frame-header picture
// types, which the chapter-stats tag scan in internal/bdrom does. The interlace
// flag on the stream comes from the format that CLPI and MPLS declare.
func ScanVC1(v *stream.VideoStream, data []byte) {
	if v.IsInitialized {
		return
	}

	var parse uint32
	var sequenceHeaderParse byte

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
			}
			v.IsVBR = true
			v.IsInitialized = true
		}
	}
}
