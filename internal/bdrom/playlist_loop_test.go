package bdrom

import (
	"testing"

	"github.com/autobrr/go-bdinfo/internal/settings"
)

// Covers the loop-detection + IsValid gating mechanism behind issue #13:
// Initialize() flags a playlist as looping when the same source clip is
// referenced at the same TimeIn more than once (a menu/"play-all" loop), and
// IsValid() then excludes it only when FilterLoopingPlaylists is enabled.
func TestPlaylistFile_HasLoopsGatesIsValid(t *testing.T) {
	cfg := settings.Default(t.TempDir())

	// Same clip referenced twice at the same TimeIn = a loop. Total length (30s)
	// clears the short-playlist threshold so only the loop filter is in play.
	pl := &PlaylistFile{
		Name:     "00001.MPLS",
		Settings: cfg,
		StreamClips: []*StreamClip{
			{AngleIndex: 0, Name: "00100.M2TS", TimeIn: 0, Length: 15},
			{AngleIndex: 0, Name: "00100.M2TS", TimeIn: 0, Length: 15},
		},
	}
	pl.Initialize()

	if !pl.HasLoops {
		t.Fatalf("expected HasLoops=true for a clip repeated at the same TimeIn")
	}

	cfg.FilterLoopingPlaylists = true
	pl.Settings = cfg
	if pl.IsValid() {
		t.Fatalf("expected IsValid()=false for a looping playlist when FilterLoopingPlaylists=true")
	}

	cfg.FilterLoopingPlaylists = false
	pl.Settings = cfg
	if !pl.IsValid() {
		t.Fatalf("expected IsValid()=true for the same playlist when FilterLoopingPlaylists=false")
	}
}
