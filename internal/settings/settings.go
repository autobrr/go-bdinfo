// Copyright (c) 2026, s0up and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package settings

import "path/filepath"

// Settings mirrors BDInfo options.
type Settings struct {
	GenerateStreamDiagnostics bool
	ExtendedStreamDiagnostics bool
	EnableSSIF                bool
	BigPlaylistOnly           bool
	FilterLoopingPlaylists    bool
	FilterShortPlaylists      bool
	FilterShortPlaylistsVal   int
	KeepStreamOrder           bool
	GenerateTextSummary       bool
	ReportFileName            string
	IncludeVersionAndNotes    bool
	GroupByTime               bool
	ForumsOnly                bool
	PlaylistOnly              string
	MainPlaylistOnly          bool
	SummaryOnly               bool
}

func Default(reportBaseDir string) Settings {
	return Settings{
		GenerateStreamDiagnostics: true,
		ExtendedStreamDiagnostics: false,
		EnableSSIF:                true,
		BigPlaylistOnly:           false,
		FilterLoopingPlaylists:    true,
		FilterShortPlaylists:      true,
		FilterShortPlaylistsVal:   20,
		KeepStreamOrder:           false,
		GenerateTextSummary:       true,
		ReportFileName:            filepath.Join(reportBaseDir, "BDInfo_{0}"),
		IncludeVersionAndNotes:    true,
		GroupByTime:               false,
		ForumsOnly:                false,
		PlaylistOnly:              "",
		MainPlaylistOnly:          false,
		SummaryOnly:               false,
	}
}
