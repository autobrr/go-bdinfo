// Copyright (c) 2026, s0up and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package codec

// RemoveEmulationBytes strips 0x03 after 0x0000 sequences.
func RemoveEmulationBytes(data []byte) []byte {
	if len(data) < 3 {
		return data
	}
	out := make([]byte, 0, len(data))
	zeroCount := 0
	for _, b := range data {
		if zeroCount >= 2 && b == 0x03 {
			zeroCount = 0
			continue
		}
		out = append(out, b)
		if b == 0x00 {
			zeroCount++
		} else {
			zeroCount = 0
		}
	}
	return out
}
