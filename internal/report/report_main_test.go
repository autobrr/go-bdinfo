// Copyright (c) 2026, s0up and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package report

import (
	"strings"
	"testing"

	"github.com/autobrr/go-bdinfo/internal/bdrom"
	"github.com/autobrr/go-bdinfo/internal/settings"
)

// Regression test for issue #13: `--main` selected a looping menu/"play-all"
// playlist because its loop-inflated length beat the real feature. Upstream
// BDInfo filters looping playlists by default; go-bdinfo must too. With default
// settings, selectMainPlaylist must drop the HasLoops playlist and return the
// real feature even though the menu playlist reports a longer total length.
func TestSelectMainPlaylist_SkipsLoopingMenuByDefault(t *testing.T) {
	cfg := settings.Default(t.TempDir())
	cfg.MainPlaylistOnly = true

	// Looping menu playlist: loop-inflated total length (longest) but HasLoops.
	menu := &bdrom.PlaylistFile{
		Name:          "01000.MPLS",
		IsInitialized: true,
		HasLoops:      true,
		Settings:      cfg,
		StreamClips: []*bdrom.StreamClip{
			{AngleIndex: 0, Length: 13558.545, PacketCount: 100_000},
		},
	}
	// Real feature: shorter runtime, legitimate (no loops).
	feature := &bdrom.PlaylistFile{
		Name:          "00800.MPLS",
		IsInitialized: true,
		HasLoops:      false,
		Settings:      cfg,
		StreamClips: []*bdrom.StreamClip{
			{AngleIndex: 0, Length: 5113.233, PacketCount: 1_000_000},
		},
	}

	got := selectMainPlaylist([]*bdrom.PlaylistFile{menu, feature}, cfg)

	if len(got) != 1 {
		t.Fatalf("expected exactly one main playlist, got %d", len(got))
	}
	if got[0].Name != "00800.MPLS" {
		t.Fatalf("expected feature 00800.MPLS as main, got looping menu %s", got[0].Name)
	}
}

func TestRenderedTimelineSelectionMatchesOutputModes(t *testing.T) {
	cfg := settings.Default(".")
	cfg.FilterLoopingPlaylists = false
	cfg.FilterShortPlaylists = false
	small := &bdrom.PlaylistFile{Name: "00001.MPLS", IsInitialized: true, Settings: cfg, StreamClips: []*bdrom.StreamClip{{Length: 100, FileSize: 1000, PacketCount: 10}}}
	big := &bdrom.PlaylistFile{Name: "00002.MPLS", IsInitialized: true, Settings: cfg, StreamClips: []*bdrom.StreamClip{{Length: 50, FileSize: 2000, PacketCount: 20}}}
	disc := &bdrom.BDROM{CaptureTimeline: true}
	for _, tc := range []struct {
		name               string
		summary, main, big bool
		want               []string
	}{{"all", false, false, false, []string{big.Name, small.Name}}, {"big full", false, false, true, []string{big.Name}}, {"big summary", true, false, true, []string{big.Name, small.Name}}, {"main", false, true, false, []string{small.Name}}, {"main summary", true, true, false, []string{small.Name}}} {
		t.Run(tc.name, func(t *testing.T) {
			settings := cfg
			settings.SummaryOnly = tc.summary
			settings.MainPlaylistOnly = tc.main
			settings.BigPlaylistOnly = tc.big
			_, out, err := RenderReport("-", disc, []*bdrom.PlaylistFile{small, big}, bdrom.ScanResult{}, settings)
			if err != nil {
				t.Fatal(err)
			}
			if len(out.Playlists) != len(tc.want) {
				t.Fatalf("selected %+v", out.Playlists)
			}
			for i, p := range out.Playlists {
				if p.Name != tc.want[i] || !strings.Contains(out.Report, p.Name) {
					t.Fatalf("selected %s text %q", p.Name, out.Report)
				}
			}
		})
	}
}

func TestScannedPlaylistSelectionUsesPacketSizes(t *testing.T) {
	for _, tc := range []struct {
		name         string
		smallPackets uint64
		bigPackets   uint64
		wantScanned  string
	}{
		{"matching sizes", 10, 20, "00002.MPLS"},
		{"scanned sizes differ from file sizes", 10, 5, "00001.MPLS"},
		{"scanned zero sizes retain name tie-break", 0, 0, "00001.MPLS"},
	} {
		for _, mode := range []string{"main", "biggest"} {
			t.Run(mode+"/"+tc.name, func(t *testing.T) {
				cfg := settings.Default(".")
				cfg.MainPlaylistOnly, cfg.BigPlaylistOnly = mode == "main", mode == "biggest"
				small := &bdrom.PlaylistFile{Name: "00001.MPLS", IsInitialized: true, Settings: cfg, StreamClips: []*bdrom.StreamClip{{Name: "00001.M2TS", Length: 100, FileSize: 1920}}}
				big := &bdrom.PlaylistFile{Name: "00002.MPLS", IsInitialized: true, Settings: cfg, StreamClips: []*bdrom.StreamClip{{Name: "00002.M2TS", Length: 100, FileSize: 3840}}}
				playlists := []*bdrom.PlaylistFile{small, big}
				small.StreamClips[0].PacketCount = tc.smallPackets
				big.StreamClips[0].PacketCount = tc.bigPackets
				_, out, err := RenderReport("-", &bdrom.BDROM{CaptureTimeline: true}, playlists, bdrom.ScanResult{}, cfg)
				if err != nil {
					t.Fatal(err)
				}
				if len(out.Playlists) != 1 || out.Playlists[0].Name != tc.wantScanned || !strings.Contains(out.Report, tc.wantScanned) {
					t.Fatalf("scanned report selected %+v, want %s", out.Playlists, tc.wantScanned)
				}
			})
		}
	}
}
