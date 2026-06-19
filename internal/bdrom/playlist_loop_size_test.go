package bdrom

import (
	"math"
	"testing"

	"github.com/autobrr/go-bdinfo/internal/settings"
)

// Issue #16: a looping menu/"play-all" playlist references the same physical clip
// (same file + in/out point) many times. Length is genuinely the sum of every
// reference, but Size must count each unique clip once -- otherwise the reported
// Size/Total Bitrate balloon far past the disc (e.g. 31.9 GB for 88 MB of content).
//
// This deliberately diverges from upstream BDInfo (which sums bytes once per loop
// iteration); the divergence only surfaces when looping playlists are shown, which
// is opt-in via --filterloopingplaylists=false.
func TestPlaylistFile_LoopingSizeCountsUniqueClipOnce(t *testing.T) {
	cfg := settings.Default(t.TempDir())

	// 300 references to the exact same clip (same name + in/out): a loop.
	const refs = 300
	clips := make([]*StreamClip, 0, refs)
	for range refs {
		clips = append(clips, &StreamClip{
			Settings:    cfg,
			AngleIndex:  0,
			Name:        "01010.M2TS",
			TimeIn:      11.651,
			TimeOut:     56.696,
			Length:      45.045,
			PacketCount: 1000,
			FileSize:    500,
		})
	}
	pl := &PlaylistFile{Name: "01000.MPLS", Settings: cfg, StreamClips: clips}

	// Length is the honest sum of every reference (unchanged from upstream).
	wantLen := 45.045 * refs
	if got := pl.TotalLength(); math.Abs(got-wantLen) > 1e-6 {
		t.Errorf("TotalLength() = %v, want %v (length must stay summed)", got, wantLen)
	}

	// Size counts the unique clip exactly once: 1000 packets * 192.
	const wantSize = 1000 * 192
	if got := pl.TotalSize(); got != wantSize {
		t.Errorf("TotalSize() = %d, want %d (each unique clip counted once)", got, wantSize)
	}

	// FileSize is likewise de-duplicated to the single physical clip.
	if got := pl.FileSize(); got != 500 {
		t.Errorf("FileSize() = %d, want 500 (de-duplicated)", got)
	}

	// Total Bitrate derives from the de-duplicated size, so it is sane (not inflated).
	wantBitrate := uint64(float64(wantSize) * 8.0 / wantLen)
	if got := pl.TotalBitRate(); got != wantBitrate {
		t.Errorf("TotalBitRate() = %d, want %d", got, wantBitrate)
	}
}

// A normal playlist (distinct clips) must be completely unaffected by the de-dup.
func TestPlaylistFile_NonLoopingSizeUnchanged(t *testing.T) {
	cfg := settings.Default(t.TempDir())
	pl := &PlaylistFile{
		Name:     "00800.MPLS",
		Settings: cfg,
		StreamClips: []*StreamClip{
			{Settings: cfg, AngleIndex: 0, Name: "00800.M2TS", TimeIn: 0, TimeOut: 10, Length: 10, PacketCount: 1000, FileSize: 500},
			{Settings: cfg, AngleIndex: 0, Name: "00801.M2TS", TimeIn: 0, TimeOut: 10, Length: 10, PacketCount: 2000, FileSize: 700},
		},
	}
	if got, want := pl.TotalSize(), uint64((1000+2000)*192); got != want {
		t.Errorf("TotalSize() = %d, want %d (distinct clips all counted)", got, want)
	}
	if got, want := pl.FileSize(), uint64(500+700); got != want {
		t.Errorf("FileSize() = %d, want %d", got, want)
	}
}

// Two references to the SAME file but DIFFERENT in/out points are distinct content
// (not a loop) and must both be counted.
func TestPlaylistFile_SameFileDifferentSegmentsBothCounted(t *testing.T) {
	cfg := settings.Default(t.TempDir())
	pl := &PlaylistFile{
		Name:     "00500.MPLS",
		Settings: cfg,
		StreamClips: []*StreamClip{
			{Settings: cfg, AngleIndex: 0, Name: "00500.M2TS", TimeIn: 0, TimeOut: 10, Length: 10, PacketCount: 1000},
			{Settings: cfg, AngleIndex: 0, Name: "00500.M2TS", TimeIn: 20, TimeOut: 30, Length: 10, PacketCount: 1000},
		},
	}
	if got, want := pl.TotalSize(), uint64((1000+1000)*192); got != want {
		t.Errorf("TotalSize() = %d, want %d (different segments both counted)", got, want)
	}
}
