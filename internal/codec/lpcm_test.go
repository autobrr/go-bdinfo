// Copyright (c) 2026, s0up and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package codec

import (
	"testing"

	"github.com/autobrr/go-bdinfo/internal/stream"
)

// A short LPCM payload must not mark the stream initialized, so a later stream
// file in the same playlist can still fill the codec fields.
func TestScanLPCM_ShortHeaderLeavesStreamUninitialized(t *testing.T) {
	a := &stream.AudioStream{}

	ScanLPCM(a, []byte{0x00, 0x00, 0x91})
	if a.IsInitialized {
		t.Fatal("short payload marked the stream initialized")
	}

	// flags 0x91C0: 3/2/1 channels, 48 kHz, 24 bits per sample.
	ScanLPCM(a, []byte{0x00, 0x00, 0x91, 0xC0})
	if !a.IsInitialized {
		t.Fatal("valid header did not initialize the stream")
	}
	if a.ChannelCount != 5 || a.LFE != 1 {
		t.Errorf("channels = %d + %d LFE, want 5 + 1", a.ChannelCount, a.LFE)
	}
	if a.BitDepth != 24 {
		t.Errorf("bit depth = %d, want 24", a.BitDepth)
	}
	if a.SampleRate != 48000 {
		t.Errorf("sample rate = %d, want 48000", a.SampleRate)
	}
	if want := int64(48000 * 24 * 6); a.BitRate != want {
		t.Errorf("bitrate = %d, want %d", a.BitRate, want)
	}
}
