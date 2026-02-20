package main

import (
	"strings"
	"testing"

	"github.com/autobrr/go-bdinfo/internal/bdrom"
)

func TestNormalizeArgs_BoolValueTokens(t *testing.T) {
	in := []string{"-m", "true", "-q", "false", "--enablessif", "TRUE", "--summaryonly", "false"}
	got := normalizeArgs(in)
	want := []string{
		"--generatetextsummary=true",
		"--includeversionandnotes=false",
		"--enablessif=true",
		"--summaryonly=false",
	}
	if len(got) != len(want) {
		t.Fatalf("len=%d want=%d; got=%q", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("idx=%d got=%q want=%q (all=%q)", i, got[i], want[i], got)
		}
	}
}

func TestNormalizePlaylistName(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{name: "empty", input: "", want: ""},
		{name: "no extension", input: "00000", want: "00000.MPLS"},
		{name: "with extension", input: "00000.mpls", want: "00000.MPLS"},
		{name: "path like", input: "PLAYLIST/00000.mpls", want: "00000.MPLS"},
		{name: "whitespace", input: "  00001.mpls  ", want: "00001.MPLS"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := normalizePlaylistName(tt.input)
			if got != tt.want {
				t.Fatalf("normalizePlaylistName(%q)=%q want=%q", tt.input, got, tt.want)
			}
		})
	}
}

func TestFilterROMToPlaylist(t *testing.T) {
	rom := &bdrom.BDROM{
		PlaylistFiles: map[string]*bdrom.PlaylistFile{
			"00000.MPLS": {Name: "00000.MPLS"},
			"00001.MPLS": {Name: "00001.MPLS"},
		},
		PlaylistOrder: []string{"00000.MPLS", "00001.MPLS"},
	}

	if err := filterROMToPlaylist(rom, "playlist/00001.mpls"); err != nil {
		t.Fatalf("filterROMToPlaylist() error = %v", err)
	}

	if len(rom.PlaylistFiles) != 1 {
		t.Fatalf("playlist files len = %d want = 1", len(rom.PlaylistFiles))
	}
	if _, ok := rom.PlaylistFiles["00001.MPLS"]; !ok {
		t.Fatalf("filtered playlist 00001.MPLS not found")
	}
	if len(rom.PlaylistOrder) != 1 || rom.PlaylistOrder[0] != "00001.MPLS" {
		t.Fatalf("playlist order = %q want = [00001.MPLS]", strings.Join(rom.PlaylistOrder, ","))
	}
}

func TestFilterROMToPlaylist_Missing(t *testing.T) {
	rom := &bdrom.BDROM{
		PlaylistFiles: map[string]*bdrom.PlaylistFile{
			"00000.MPLS": {Name: "00000.MPLS"},
		},
		PlaylistOrder: []string{"00000.MPLS"},
	}

	err := filterROMToPlaylist(rom, "00077")
	if err == nil {
		t.Fatal("expected error for missing playlist")
	}
	if !strings.Contains(err.Error(), "playlist not found") {
		t.Fatalf("unexpected error: %v", err)
	}
}
