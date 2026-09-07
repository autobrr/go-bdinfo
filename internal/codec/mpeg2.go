// Copyright (c) 2026, s0up and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package codec

import "github.com/autobrr/go-bdinfo/internal/stream"

func ScanMPEG2(v *stream.VideoStream, _ []byte) {
	if v.IsInitialized {
		return
	}
	v.IsVBR = true
	v.IsInitialized = true
}
