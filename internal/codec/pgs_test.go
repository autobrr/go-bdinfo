// Copyright (c) 2026, s0up and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package codec

import (
	"testing"

	"github.com/autobrr/go-bdinfo/internal/stream"
)

func pgsPCS(forcedFlags ...byte) []byte {
	seg := []byte{0x07, 0x80, 0x04, 0x38, 0x10, 0x00, 0x01, 0x80, 0x00, 0x00, byte(len(forcedFlags))}
	for _, flags := range forcedFlags {
		seg = append(seg, 0x00, 0x00, 0x00, flags, 0, 0, 0, 0)
		if flags&0x80 == 0x80 {
			seg = append(seg, 0, 0, 0, 0, 0, 0, 0, 0)
		}
	}
	return append([]byte{0x16, byte(len(seg) >> 8), byte(len(seg))}, seg...)
}

func TestScanPGS(t *testing.T) {
	ods := []byte{0x15, 0x00, 0x04, 0x00, 0x00, 0x00, 0x00}
	end := []byte{0x80, 0x00, 0x00}
	g := stream.NewGraphicsStream()
	for _, transfer := range [][]byte{
		pgsPCS(0xC0, 0x00), ods, end, // cropped forced object, then plain: last object wins
		pgsPCS(0x00, 0x40), ods, ods, end, // forced, ODS split in two fragments counts twice
		pgsPCS(), ods, // empty composition after END: not counted
	} {
		ScanPGS(g, transfer)
	}
	if got, want := g.Description(), "1920x1080 / 1 Caption (2 Forced Captions)"; got != want {
		t.Fatalf("description = %q, want %q", got, want)
	}
}

// A PCS truncated before the video size must not mark the stream initialized,
// so a later, complete PCS can still fill Width and Height.
func TestScanPGS_TruncatedPCSLeavesStreamUninitialized(t *testing.T) {
	g := stream.NewGraphicsStream()

	ScanPGS(g, []byte{0x16, 0x00, 0x07, 0x80})
	if g.IsInitialized {
		t.Fatal("truncated PCS marked the stream initialized")
	}

	ScanPGS(g, pgsPCS(0x00))
	if !g.IsInitialized {
		t.Fatal("complete PCS did not initialize the stream")
	}
	if g.Width != 1920 || g.Height != 1080 {
		t.Errorf("size = %dx%d, want 1920x1080", g.Width, g.Height)
	}
}
