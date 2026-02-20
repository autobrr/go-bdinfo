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
		FilterLoopingPlaylists:    false,
		FilterShortPlaylists:      true,
		FilterShortPlaylistsVal:   20,
		KeepStreamOrder:           false,
		GenerateTextSummary:       true,
		ReportFileName:            filepath.Join(reportBaseDir, "BDInfo_{0}.bdinfo"),
		IncludeVersionAndNotes:    false,
		GroupByTime:               false,
		ForumsOnly:                false,
		PlaylistOnly:              "",
		MainPlaylistOnly:          false,
		SummaryOnly:               false,
	}
}
