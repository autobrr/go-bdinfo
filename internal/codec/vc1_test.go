// Copyright (c) 2026, s0up and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package codec

import (
	"testing"

	"github.com/autobrr/go-bdinfo/internal/stream"
)

// The sequence-header interlace bit is local to the scan, as in TSCodecVC1.cs,
// so the format that CLPI and MPLS declare stays on the stream.
func TestScanVC1_KeepsDeclaredInterlace(t *testing.T) {
	v := &stream.VideoStream{}
	v.SetVideoFormat(stream.VideoFormat1080i)

	// Advanced profile, level 3; the last byte has the interlace bit (0x40) clear.
	ScanVC1(v, []byte{0x00, 0x00, 0x01, 0x0F, 0xD8, 0x00, 0x00, 0x00, 0x00, 0x00})

	if !v.IsInterlaced {
		t.Fatal("IsInterlaced = false, want the declared 1080i to stay")
	}
	if got, want := v.EncodingProfile, "Advanced Profile 3"; got != want {
		t.Fatalf("EncodingProfile = %q, want %q", got, want)
	}
	if !v.IsVBR || !v.IsInitialized {
		t.Fatalf("IsVBR = %v, IsInitialized = %v, want both true", v.IsVBR, v.IsInitialized)
	}
}
