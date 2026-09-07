// Copyright (c) 2026, s0up and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package codec

import "github.com/autobrr/go-bdinfo/internal/stream"

func ScanAVC(v *stream.VideoStream, data []byte, tag *string) {
	parse := uint32(0)
	accessUnit := 0
	seqParse := 0
	profile := ""
	var constraintSet3 byte

	for i := range data {
		parse = (parse << 8) | uint32(data[i])
		if parse == 0x00000109 {
			accessUnit = 1
			continue
		}
		if accessUnit > 0 {
			accessUnit--
			if accessUnit == 0 {
				unitType := (parse & 0xFF) >> 5
				switch unitType {
				case 0, 3, 5:
					if tag != nil {
						*tag = "I"
					}
				case 1, 4, 6:
					if tag != nil {
						*tag = "P"
					}
				case 2, 7:
					if tag != nil {
						*tag = "B"
					}
				}
				if v.IsInitialized {
					return
				}
			}
			continue
		}
		if parse == 0x00000127 || parse == 0x00000167 {
			seqParse = 3
			continue
		}
		if seqParse > 0 {
			seqParse--
			switch seqParse {
			case 2:
				switch parse & 0xFF {
				case 66:
					profile = "Baseline Profile"
				case 77:
					profile = "Main Profile"
				case 88:
					profile = "Extended Profile"
				case 100:
					profile = "High Profile"
				case 110:
					profile = "High 10 Profile"
				case 122:
					profile = "High 4:2:2 Profile"
				case 144:
					profile = "High 4:4:4 Profile"
				default:
					profile = "Unknown Profile"
				}
			case 1:
				constraintSet3 = byte((parse & 0x10) >> 4)
			case 0:
				b := byte(parse & 0xFF)
				level := ""
				if b == 11 && constraintSet3 == 1 {
					level = "1b"
				} else {
					level = string([]byte{byte('0' + b/10), '.', byte('0' + b%10)})
				}
				v.EncodingProfile = profile + " " + level
				v.IsVBR = true
				v.IsInitialized = true
				return
			}
		}
	}
}
