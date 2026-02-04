package bdrom

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/autobrr/go-bdinfo/internal/settings"
)

// TestPlaylistFile_TotalLength tests duration calculation
func TestPlaylistFile_TotalLength(t *testing.T) {
	tests := []struct {
		name     string
		clips    []*StreamClip
		expected float64
	}{
		{
			name: "single clip",
			clips: []*StreamClip{
				{Length: 100.0, AngleIndex: 0},
			},
			expected: 100.0,
		},
		{
			name: "multiple clips",
			clips: []*StreamClip{
				{Length: 100.0, AngleIndex: 0},
				{Length: 200.0, AngleIndex: 0},
				{Length: 50.0, AngleIndex: 0},
			},
			expected: 350.0,
		},
		{
			name: "clips with angles",
			clips: []*StreamClip{
				{Length: 100.0, AngleIndex: 0},
				{Length: 50.0, AngleIndex: 1}, // Angle, shouldn't count
				{Length: 200.0, AngleIndex: 0},
			},
			expected: 300.0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			playlist := &PlaylistFile{
				StreamClips: tt.clips,
			}
			got := playlist.TotalLength()
			if got != tt.expected {
				t.Errorf("TotalLength() = %.1f, want %.1f", got, tt.expected)
			}
		})
	}
}

// TestPlaylistFile_TotalAngleLength tests total length including angles
func TestPlaylistFile_TotalAngleLength(t *testing.T) {
	playlist := &PlaylistFile{
		StreamClips: []*StreamClip{
			{Length: 100.0, AngleIndex: 0},
			{Length: 50.0, AngleIndex: 1},
			{Length: 200.0, AngleIndex: 0},
		},
	}

	expected := 350.0 // All clips including angles
	got := playlist.TotalAngleLength()
	if got != expected {
		t.Errorf("TotalAngleLength() = %.1f, want %.1f", got, expected)
	}
}

// TestPlaylistFile_FileSize tests total file size calculation
func TestPlaylistFile_FileSize(t *testing.T) {
	tests := []struct {
		name     string
		clips    []*StreamClip
		expected uint64
	}{
		{
			name: "single file",
			clips: []*StreamClip{
				{FileSize: 1000000},
			},
			expected: 1000000,
		},
		{
			name: "multiple files",
			clips: []*StreamClip{
				{FileSize: 1000000},
				{FileSize: 2000000},
				{FileSize: 500000},
			},
			expected: 3500000,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			playlist := &PlaylistFile{
				StreamClips: tt.clips,
			}
			got := playlist.FileSize()
			if got != tt.expected {
				t.Errorf("FileSize() = %d, want %d", got, tt.expected)
			}
		})
	}
}

// TestPlaylistFile_IsValid tests playlist validation
func TestPlaylistFile_IsValid(t *testing.T) {
	tests := []struct {
		name     string
		playlist *PlaylistFile
		want     bool
	}{
		{
			name: "valid long playlist",
			playlist: &PlaylistFile{
				IsInitialized: true,
				StreamClips:   []*StreamClip{{Length: 3600, AngleIndex: 0}}, // 1 hour
				Settings:      settings.Settings{FilterShortPlaylists: true, FilterShortPlaylistsVal: 20},
			},
			want: true,
		},
		{
			name: "too short",
			playlist: &PlaylistFile{
				IsInitialized: true,
				StreamClips:   []*StreamClip{{Length: 10, AngleIndex: 0}}, // 10 seconds
				Settings:      settings.Settings{FilterShortPlaylists: true, FilterShortPlaylistsVal: 20},
			},
			want: false,
		},
		{
			name: "short filter disabled",
			playlist: &PlaylistFile{
				IsInitialized: true,
				StreamClips:   []*StreamClip{{Length: 10, AngleIndex: 0}},
				Settings:      settings.Settings{FilterShortPlaylists: false, FilterShortPlaylistsVal: 20},
			},
			want: true,
		},
		{
			name: "has loops with filter enabled",
			playlist: &PlaylistFile{
				IsInitialized: true,
				HasLoops:      true,
				StreamClips:   []*StreamClip{{Length: 3600, AngleIndex: 0}},
				Settings:      settings.Settings{FilterLoopingPlaylists: true},
			},
			want: false,
		},
		{
			name: "has loops with filter disabled",
			playlist: &PlaylistFile{
				IsInitialized: true,
				HasLoops:      true,
				StreamClips:   []*StreamClip{{Length: 3600, AngleIndex: 0}},
				Settings:      settings.Settings{FilterLoopingPlaylists: false},
			},
			want: true,
		},
		{
			name: "not initialized",
			playlist: &PlaylistFile{
				IsInitialized: false,
				StreamClips:   []*StreamClip{{Length: 3600, AngleIndex: 0}},
			},
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.playlist.IsValid()
			if got != tt.want {
				t.Errorf("IsValid() = %v, want %v", got, tt.want)
			}
		})
	}
}

// TestPlaylistFile_TotalBitRate tests bitrate calculation
func TestPlaylistFile_TotalBitRate(t *testing.T) {
	tests := []struct {
		name     string
		playlist *PlaylistFile
		expected uint64
	}{
		{
			name: "zero length",
			playlist: &PlaylistFile{
				StreamClips: []*StreamClip{
					{Length: 0, AngleIndex: 0},
				},
			},
			expected: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.playlist.TotalBitRate()
			if got != tt.expected {
				t.Errorf("TotalBitRate() = %d, want %d", got, tt.expected)
			}
		})
	}
}

// TestPlaylistFile_Scan_Integration tests playlist parsing with real Blu-ray discs.
//
// To run this test, set the BDINFO_TEST_PATH environment variable to either:
//
//   - A single Blu-ray disc path (containing BDMV folder)
//
//   - A folder containing multiple Blu-ray disc subfolders
//
//     Windows PowerShell: $env:BDINFO_TEST_PATH="D:\Movies"
//     Windows CMD:        set BDINFO_TEST_PATH=D:\Movies
//     Linux/Mac:          export BDINFO_TEST_PATH="/path/to/movies"
//
// Then run: go test -v ./internal/bdrom -run TestPlaylistFile_Scan_Integration
func TestPlaylistFile_Scan_Integration(t *testing.T) {
	bdPath := os.Getenv("BDINFO_TEST_PATH")
	if bdPath == "" {
		t.Skip("BDINFO_TEST_PATH not set, skipping integration test")
	}

	// Check if it's a single Blu-ray disc or a folder of discs
	singleDiscPlaylistPath := filepath.Join(bdPath, "BDMV", "PLAYLIST")
	isSingleDisc := false
	if _, err := os.Stat(singleDiscPlaylistPath); err == nil {
		isSingleDisc = true
	}

	var discPaths []string
	if isSingleDisc {
		discPaths = []string{bdPath}
	} else {
		// Find all subdirectories containing BDMV folders
		entries, err := os.ReadDir(bdPath)
		if err != nil {
			t.Fatalf("Failed to read directory %s: %v", bdPath, err)
		}

		for _, entry := range entries {
			if !entry.IsDir() {
				continue
			}
			subPath := filepath.Join(bdPath, entry.Name())
			bdmvPath := filepath.Join(subPath, "BDMV", "PLAYLIST")
			if _, err := os.Stat(bdmvPath); err == nil {
				discPaths = append(discPaths, subPath)
			}
		}

		if len(discPaths) == 0 {
			t.Skipf("No Blu-ray discs found in %s", bdPath)
		}
	}

	t.Logf("Testing %d Blu-ray disc(s)", len(discPaths))

	for _, discPath := range discPaths {
		discName := filepath.Base(discPath)
		t.Run(discName, func(t *testing.T) {
			testSingleDisc(t, discPath, discName)
		})
	}
}

func testSingleDisc(t *testing.T, bdPath, discName string) {
	t.Logf("Testing Blu-ray: %s", discName)

	rom, err := New(bdPath, settings.Default("."))
	if err != nil {
		t.Fatalf("Failed to open BD structure: %v", err)
	}
	defer rom.Close()

	if len(rom.PlaylistFiles) == 0 {
		t.Fatal("No playlist files found")
	}

	// Scan all playlists
	for _, pl := range rom.PlaylistFiles {
		err := pl.Scan(rom.StreamFiles, rom.StreamClipFiles)
		if err != nil {
			t.Logf("Warning: Failed to scan playlist %s: %v", pl.Name, err)
			continue
		}
	}

	// Test selectMainPlaylist logic - it should pick the playlist with best composite score
	// For Hold.Me.Back.2020, it should select 00010.MPLS (2h 14min, 40GB) over shorter playlists
	candidatePlaylists := make([]*PlaylistFile, 0, len(rom.PlaylistFiles))
	for _, pl := range rom.PlaylistFiles {
		if pl.IsInitialized {
			candidatePlaylists = append(candidatePlaylists, pl)
		}
	}

	if len(candidatePlaylists) == 0 {
		t.Fatal("No successfully scanned playlists found")
	}

	// Helper function to get largest file size (matching report.go logic)
	largestFileSize := func(pl *PlaylistFile) uint64 {
		var maxSize uint64
		for _, clip := range pl.StreamClips {
			if clip.AngleIndex == 0 && clip.FileSize > maxSize {
				maxSize = clip.FileSize
			}
		}
		return maxSize
	}

	// Log all playlists for debugging
	t.Log("\nAll playlists found:")
	for _, pl := range candidatePlaylists {
		largestFile := largestFileSize(pl)
		totalSize := pl.FileSize()
		duration := pl.TotalLength()
		fileConcentration := float64(0)
		if totalSize > 0 {
			fileConcentration = float64(largestFile) / float64(totalSize)
		}

		// Calculate score using same logic as report.go
		maxFileSize := 100.0 * 1024 * 1024 * 1024
		maxTotalSize := 150.0 * 1024 * 1024 * 1024
		maxDuration := 14400.0

		score := (float64(largestFile)/maxFileSize)*40.0 +
			(float64(totalSize)/maxTotalSize)*30.0 +
			(duration/maxDuration)*20.0 +
			fileConcentration*10.0

		t.Logf("  %s: duration=%.1fs, totalSize=%d, largestFile=%d, concentration=%.3f, score=%.2f",
			pl.Name, duration, totalSize, largestFile, fileConcentration, score)
	}

	// Find the playlist with highest score (main feature)
	var mainPlaylist *PlaylistFile
	maxScore := -1.0
	for _, pl := range candidatePlaylists {
		largestFile := largestFileSize(pl)
		totalSize := pl.FileSize()
		duration := pl.TotalLength()
		fileConcentration := float64(0)
		if totalSize > 0 {
			fileConcentration = float64(largestFile) / float64(totalSize)
		}

		maxFileSize := 100.0 * 1024 * 1024 * 1024
		maxTotalSize := 150.0 * 1024 * 1024 * 1024
		maxDuration := 14400.0

		score := (float64(largestFile)/maxFileSize)*40.0 +
			(float64(totalSize)/maxTotalSize)*30.0 +
			(duration/maxDuration)*20.0 +
			fileConcentration*10.0

		if score > maxScore {
			maxScore = score
			mainPlaylist = pl
		}
	}

	if mainPlaylist == nil {
		t.Fatal("Failed to select main playlist")
	}

	t.Logf("\nSelected main playlist: %s (score: %.2f)", mainPlaylist.Name, maxScore)
	t.Logf("  Type: %s", mainPlaylist.FileType)
	t.Logf("  Clips: %d", len(mainPlaylist.StreamClips))
	t.Logf("  Duration: %.1f seconds (%.1f minutes)", mainPlaylist.TotalLength(), mainPlaylist.TotalLength()/60)
	t.Logf("  File Size: %d bytes (%.1f GB)", mainPlaylist.FileSize(), float64(mainPlaylist.FileSize())/(1024*1024*1024))
	t.Logf("  Largest File: %d bytes (%.1f GB)", largestFileSize(mainPlaylist), float64(largestFileSize(mainPlaylist))/(1024*1024*1024))

	// Verify main feature is reasonably long (should be at least 30 minutes for a movie)
	if mainPlaylist.TotalLength() < 1800 {
		t.Errorf("Main playlist duration is suspiciously short: %.1f seconds", mainPlaylist.TotalLength())
	}

	if len(mainPlaylist.StreamClips) == 0 {
		t.Error("Expected at least one stream clip")
	}

	if mainPlaylist.FileType == "" {
		t.Error("FileType should not be empty")
	}
}
