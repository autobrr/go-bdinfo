package report

import (
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
