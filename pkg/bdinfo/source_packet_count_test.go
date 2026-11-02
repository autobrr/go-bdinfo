// Copyright (c) 2026, s0up and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package bdinfo_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/autobrr/go-bdinfo/pkg/bdinfo"
	"github.com/autobrr/go-bdinfo/pkg/bdinfo/video"
)

func TestCollectionPhysicalSourcePackets(t *testing.T) {
	for _, tc := range []struct {
		name                string
		bare, tail, partial bool
	}{
		{name: "M2TS"},
		{name: "bare-TS", bare: true},
		{name: "unselected-adaptation-tail", tail: true},
		{name: "partial-final-packet", partial: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := discFixture(t)
			path := filepath.Join(root, "BDMV", "STREAM", "00001.m2ts")
			data, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			want := uint64(len(data) / 192)
			if tc.tail {
				packet := make([]byte, 192)
				packet[4], packet[5], packet[6], packet[7], packet[8] = 0x47, 0x1f, 0xfe, 0x20, 183
				for i := 10; i < len(packet); i++ {
					packet[i] = 0xff
				}
				data = append(data, packet...)
				want++
			}
			if tc.bare {
				var bare []byte
				for i := 0; i < len(data); i += 192 {
					bare = append(bare, data[i+4:i+192]...)
				}
				data = bare
			}
			if tc.partial {
				data = append(data, 0, 0, 0)
			}
			if err := os.WriteFile(path, data, 0644); err != nil {
				t.Fatal(err)
			}
			cfg := bdinfo.DefaultSettings(".")
			cfg.FilterLoopingPlaylists, cfg.FilterShortPlaylists, cfg.BigPlaylistOnly = false, false, false
			r := &recorder{}
			out, err := bdinfo.Run(t.Context(), bdinfo.Options{Path: root, Settings: cfg, VideoConsumer: func(context.Context, video.StreamInfo) (video.Consumer, error) { return r, nil }})
			if err != nil {
				t.Fatal(err)
			}
			if len(r.ends) != 1 || len(out.Collection) != 1 {
				t.Fatalf("terminal delivery: %+v %+v", r.ends, out.Collection)
			}
			end, result := r.ends[0], out.Collection[0]
			if tc.partial {
				if end.CleanEOF || result.CleanEOF || end.SourcePacketCount != 0 || result.SourcePacketCount != 0 {
					t.Fatalf("partial EOF published extent: %+v %+v", end, result)
				}
			} else if !end.CleanEOF || !result.CleanEOF || end.SourcePacketCount != want || result.SourcePacketCount != want {
				t.Fatalf("observed physical EOF: end %+v result %+v want %d", end, result, want)
			}
		})
	}
}
