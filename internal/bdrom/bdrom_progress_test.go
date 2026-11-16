// Copyright (c) 2026, s0up and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package bdrom

import (
	"bytes"
	"encoding/binary"
	"testing"

	"github.com/autobrr/go-bdinfo/internal/settings"
)

func TestScanProgress_UsesSelectedStreamSourceSize(t *testing.T) {
	packet := make([]byte, 192)
	ts := tsPacket188(0x1fff, false, nil)
	copy(packet[4:], ts[:])
	m2ts := bytes.Repeat(packet, 2)
	ssif := bytes.Repeat(packet, 4)
	for _, tc := range []struct {
		name        string
		enableSSIF  bool
		interleaved bool
		missingInfo bool
		cachedSSIF  bool
		wantBytes   uint64
	}{
		{name: "SSIF", enableSSIF: true, interleaved: true, wantBytes: 768},
		{name: "SSIF disabled", interleaved: true, wantBytes: 384},
		{name: "SSIF absent", enableSSIF: true, wantBytes: 384},
		{name: "SSIF file info absent", enableSSIF: true, interleaved: true, missingInfo: true, wantBytes: 384},
		{name: "cached size from SSIF scan", interleaved: true, cachedSSIF: true, wantBytes: 384},
	} {
		t.Run(tc.name, func(t *testing.T) {
			sf := NewStreamFile(&memFileInfo{name: "00001.M2TS", data: m2ts})
			if tc.cachedSSIF {
				sf.Size = int64(len(ssif))
			}
			unused := NewStreamFile(&memFileInfo{name: "00002.M2TS", data: ssif})
			clpi := make([]byte, 36)
			copy(clpi, "HDMV0200")
			binary.BigEndian.PutUint32(clpi[12:], 20)
			binary.BigEndian.PutUint32(clpi[20:], 12)
			cf := NewStreamClipFile(&memFileInfo{name: "00001.CLPI", data: clpi})
			rom := &BDROM{
				StreamFiles:      map[string]*StreamFile{sf.Name: sf, unused.Name: unused},
				StreamClipFiles:  map[string]*StreamClipFile{cf.Name: cf},
				PlaylistFiles:    make(map[string]*PlaylistFile),
				InterleavedFiles: make(map[string]*InterleavedFile),
			}
			if tc.interleaved {
				interleaved := &InterleavedFile{Name: "00001.SSIF", Size: int64(len(ssif))}
				if !tc.missingInfo {
					interleaved.FileInfo = &memFileInfo{name: interleaved.Name, data: ssif}
				}
				rom.InterleavedFiles[interleaved.Name] = interleaved
			}
			// Shared playlists and repeated play items still scan the source once.
			for _, name := range []string{"00000.MPLS", "00001.MPLS"} {
				rom.PlaylistFiles[name] = NewPlaylistFile(&memFileInfo{name: name, data: timelineFixture(1, 45001, 2)}, settings.Settings{EnableSSIF: tc.enableSSIF})
			}
			var updates []ScanProgress
			result := rom.ScanWithProgress(t.Context(), func(update ScanProgress) {
				if update.Stage == ScanStageStream {
					updates = append(updates, update)
				}
			})
			if result.ScanError != nil || len(result.FileErrors) != 0 {
				t.Fatalf("scan: %+v", result)
			}
			if len(updates) < 2 || updates[0].ProcessedBytes != 0 {
				t.Fatalf("missing initial/final stream progress: %+v", updates)
			}
			var previous uint64
			for _, update := range updates {
				if update.Total != 1 || update.TotalBytes != tc.wantBytes || update.ProcessedBytes > update.TotalBytes || update.ProcessedBytes < previous {
					t.Fatalf("stream progress = %+v, want one source, %d total bytes and monotonic progress within total", update, tc.wantBytes)
				}
				previous = update.ProcessedBytes
			}
			last := updates[len(updates)-1]
			if last.Completed != 1 || last.ProcessedBytes != tc.wantBytes {
				t.Fatalf("final stream progress = %+v, want one completed source and %d processed bytes", last, tc.wantBytes)
			}
		})
	}
}
