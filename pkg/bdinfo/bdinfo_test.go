package bdinfo

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

// DiscoverPlaylists must bracket its progress stream like Run does:
// StageStarting first, StageDone last.
func TestDiscoverPlaylists_EmitsStartingAndDone(t *testing.T) {
	root := t.TempDir()
	for _, dir := range []string{"BDMV/PLAYLIST", "BDMV/CLIPINF"} {
		if err := os.MkdirAll(filepath.Join(root, dir), 0o755); err != nil {
			t.Fatal(err)
		}
	}

	var stages []Stage
	_, err := DiscoverPlaylists(context.Background(), Options{
		Path:       root,
		OnProgress: func(e ProgressEvent) { stages = append(stages, e.Stage) },
	})
	if err != nil {
		t.Fatal(err)
	}

	if len(stages) == 0 || stages[0] != StageStarting {
		t.Fatalf("stages = %v, want StageStarting first", stages)
	}
	if stages[len(stages)-1] != StageDone {
		t.Fatalf("stages = %v, want StageDone last", stages)
	}
}
