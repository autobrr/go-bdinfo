package bdinfo

import (
	"context"
	"errors"
	"time"

	"github.com/autobrr/go-bdinfo/internal/bdrom"
	"github.com/autobrr/go-bdinfo/internal/report"
	internalsettings "github.com/autobrr/go-bdinfo/internal/settings"
)

// Stage represents a coarse progress stage for Run.
type Stage string

const (
	StageStarting        Stage = "starting"
	StageDiscovered      Stage = "discovered"
	StageScanning        Stage = "scanning"
	StageClipInfo        Stage = "clipinfo"
	StagePlaylist        Stage = "playlist"
	StageStream          Stage = "stream"
	StageInitialize      Stage = "initialize"
	StageScanComplete    Stage = "scan_complete"
	StageRenderingReport Stage = "rendering_report"
	StageDone            Stage = "done"
)

// ProgressEvent is emitted when Run transitions between major phases.
type ProgressEvent struct {
	Stage          Stage
	Path           string
	Playlists      int
	ClipInfos      int
	Streams        int
	Completed      int
	Total          int
	TotalBytes     uint64
	ProcessedBytes uint64
	Elapsed        time.Duration
	OccurredAt     time.Time
}

// Settings are library-facing scan and report controls.
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

// DefaultSettings returns library defaults equivalent to CLI defaults.
func DefaultSettings(reportBaseDir string) Settings {
	base := internalsettings.Default(reportBaseDir)
	return fromInternalSettings(base)
}

// Options configure one Run call for a single disc folder or ISO path.
type Options struct {
	Path       string
	ReportPath string
	Settings   Settings
	OnProgress func(ProgressEvent)
}

// DiscInfo contains high-level disc metadata.
type DiscInfo struct {
	Path      string
	Title     string
	Label     string
	SizeBytes uint64
	IsBDPlus  bool
	IsBDJava  bool
	IsDBOX    bool
	IsPSP     bool
	Is3D      bool
	Is50Hz    bool
	IsUHD     bool
}

// PlaylistInfo contains top-level playlist metrics.
type PlaylistInfo struct {
	Name            string
	LengthSeconds   float64
	SizeBytes       uint64
	TotalBitrateBps uint64
	HasHiddenTracks bool
	IsValid         bool
}

// ScanInfo exposes non-fatal scan errors captured during Run.
type ScanInfo struct {
	ScanError  string
	FileErrors map[string]string
}

// Result contains structured scan output plus rendered report content.
type Result struct {
	Disc       DiscInfo
	Playlists  []PlaylistInfo
	Scan       ScanInfo
	Report     string
	ReportPath string
}

// Run scans one path and returns structured output plus report content.
// The API does not write files; callers own output persistence behavior.
func Run(ctx context.Context, options Options) (Result, error) {
	if options.Path == "" {
		return Result{}, errors.New("path is required")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return Result{}, err
	}

	start := time.Now()
	emit(options.OnProgress, ProgressEvent{
		Stage:      StageStarting,
		Path:       options.Path,
		OccurredAt: time.Now(),
	})

	cfg := toInternalSettings(options.Settings)
	rom, err := bdrom.New(options.Path, cfg)
	if err != nil {
		return Result{}, err
	}
	defer rom.Close()

	if err := filterROMToPlaylist(rom, cfg.PlaylistOnly); err != nil {
		return Result{}, err
	}

	emit(options.OnProgress, ProgressEvent{
		Stage:      StageDiscovered,
		Path:       options.Path,
		Playlists:  len(rom.PlaylistFiles),
		ClipInfos:  len(rom.StreamClipFiles),
		Streams:    len(rom.StreamFiles),
		OccurredAt: time.Now(),
	})

	if err := ctx.Err(); err != nil {
		return Result{}, err
	}

	emit(options.OnProgress, ProgressEvent{
		Stage:      StageScanning,
		Path:       options.Path,
		OccurredAt: time.Now(),
	})
	var scan bdrom.ScanResult
	if options.OnProgress != nil {
		scan = rom.ScanWithProgress(func(update bdrom.ScanProgress) {
			stage, ok := stageFromScanProgress(update.Stage)
			if !ok {
				return
			}
			emit(options.OnProgress, ProgressEvent{
				Stage:          stage,
				Path:           options.Path,
				Completed:      update.Completed,
				Total:          update.Total,
				ProcessedBytes: update.ProcessedBytes,
				TotalBytes:     update.TotalBytes,
				OccurredAt:     time.Now(),
			})
		})
	} else {
		scan = rom.Scan()
	}

	emit(options.OnProgress, ProgressEvent{
		Stage:      StageScanComplete,
		Path:       options.Path,
		Completed:  1,
		Total:      1,
		Elapsed:    time.Since(start),
		OccurredAt: time.Now(),
	})

	if err := ctx.Err(); err != nil {
		return Result{}, err
	}

	playlists := orderedPlaylists(rom)
	emit(options.OnProgress, ProgressEvent{
		Stage:      StageRenderingReport,
		Path:       options.Path,
		OccurredAt: time.Now(),
	})

	reportPath, reportText, err := report.RenderReport(options.ReportPath, rom, playlists, scan, cfg)
	if err != nil {
		return Result{}, err
	}

	result := Result{
		Disc:       buildDiscInfo(rom),
		Playlists:  buildPlaylistInfo(playlists),
		Scan:       buildScanInfo(scan),
		Report:     reportText,
		ReportPath: reportPath,
	}

	emit(options.OnProgress, ProgressEvent{
		Stage:      StageDone,
		Path:       options.Path,
		Elapsed:    time.Since(start),
		OccurredAt: time.Now(),
	})

	return result, nil
}

func emit(cb func(ProgressEvent), event ProgressEvent) {
	if cb != nil {
		cb(event)
	}
}

func stageFromScanProgress(stage bdrom.ScanProgressStage) (Stage, bool) {
	switch stage {
	case bdrom.ScanStageClipInfo:
		return StageClipInfo, true
	case bdrom.ScanStagePlaylist:
		return StagePlaylist, true
	case bdrom.ScanStageStream:
		return StageStream, true
	case bdrom.ScanStageInitialize:
		return StageInitialize, true
	default:
		return "", false
	}
}

func orderedPlaylists(rom *bdrom.BDROM) []*bdrom.PlaylistFile {
	playlists := make([]*bdrom.PlaylistFile, 0, len(rom.PlaylistFiles))
	if len(rom.PlaylistOrder) > 0 {
		for _, name := range rom.PlaylistOrder {
			if pl, ok := rom.PlaylistFiles[name]; ok {
				playlists = append(playlists, pl)
			}
		}
		return playlists
	}
	for _, pl := range rom.PlaylistFiles {
		playlists = append(playlists, pl)
	}
	return playlists
}

func buildDiscInfo(rom *bdrom.BDROM) DiscInfo {
	return DiscInfo{
		Path:      rom.Path,
		Title:     rom.DiscTitle,
		Label:     rom.VolumeLabel,
		SizeBytes: rom.Size,
		IsBDPlus:  rom.IsBDPlus,
		IsBDJava:  rom.IsBDJava,
		IsDBOX:    rom.IsDBOX,
		IsPSP:     rom.IsPSP,
		Is3D:      rom.Is3D,
		Is50Hz:    rom.Is50Hz,
		IsUHD:     rom.IsUHD,
	}
}

func buildPlaylistInfo(playlists []*bdrom.PlaylistFile) []PlaylistInfo {
	out := make([]PlaylistInfo, 0, len(playlists))
	for _, playlist := range playlists {
		if playlist == nil {
			continue
		}
		out = append(out, PlaylistInfo{
			Name:            playlist.Name,
			LengthSeconds:   playlist.TotalLength(),
			SizeBytes:       playlist.TotalSize(),
			TotalBitrateBps: playlist.TotalBitRate(),
			HasHiddenTracks: playlist.HasHiddenTracks,
			IsValid:         playlist.IsValid(),
		})
	}
	return out
}

func buildScanInfo(scan bdrom.ScanResult) ScanInfo {
	info := ScanInfo{FileErrors: make(map[string]string, len(scan.FileErrors))}
	if scan.ScanError != nil {
		info.ScanError = scan.ScanError.Error()
	}
	for name, err := range scan.FileErrors {
		if err == nil {
			continue
		}
		info.FileErrors[name] = err.Error()
	}
	return info
}

func fromInternalSettings(s internalsettings.Settings) Settings {
	return Settings{
		GenerateStreamDiagnostics: s.GenerateStreamDiagnostics,
		ExtendedStreamDiagnostics: s.ExtendedStreamDiagnostics,
		EnableSSIF:                s.EnableSSIF,
		BigPlaylistOnly:           s.BigPlaylistOnly,
		FilterLoopingPlaylists:    s.FilterLoopingPlaylists,
		FilterShortPlaylists:      s.FilterShortPlaylists,
		FilterShortPlaylistsVal:   s.FilterShortPlaylistsVal,
		KeepStreamOrder:           s.KeepStreamOrder,
		GenerateTextSummary:       s.GenerateTextSummary,
		ReportFileName:            s.ReportFileName,
		IncludeVersionAndNotes:    s.IncludeVersionAndNotes,
		GroupByTime:               s.GroupByTime,
		ForumsOnly:                s.ForumsOnly,
		PlaylistOnly:              s.PlaylistOnly,
		MainPlaylistOnly:          s.MainPlaylistOnly,
		SummaryOnly:               s.SummaryOnly,
	}
}

func toInternalSettings(s Settings) internalsettings.Settings {
	return internalsettings.Settings{
		GenerateStreamDiagnostics: s.GenerateStreamDiagnostics,
		ExtendedStreamDiagnostics: s.ExtendedStreamDiagnostics,
		EnableSSIF:                s.EnableSSIF,
		BigPlaylistOnly:           s.BigPlaylistOnly,
		FilterLoopingPlaylists:    s.FilterLoopingPlaylists,
		FilterShortPlaylists:      s.FilterShortPlaylists,
		FilterShortPlaylistsVal:   s.FilterShortPlaylistsVal,
		KeepStreamOrder:           s.KeepStreamOrder,
		GenerateTextSummary:       s.GenerateTextSummary,
		ReportFileName:            s.ReportFileName,
		IncludeVersionAndNotes:    s.IncludeVersionAndNotes,
		GroupByTime:               s.GroupByTime,
		ForumsOnly:                s.ForumsOnly,
		PlaylistOnly:              s.PlaylistOnly,
		MainPlaylistOnly:          s.MainPlaylistOnly,
		SummaryOnly:               s.SummaryOnly,
	}
}

func filterROMToPlaylist(rom *bdrom.BDROM, playlistName string) error {
	if rom == nil || playlistName == "" {
		return nil
	}
	pl, ok := rom.PlaylistFiles[playlistName]
	if !ok {
		return errors.New("playlist not found: " + playlistName)
	}
	rom.PlaylistFiles = map[string]*bdrom.PlaylistFile{playlistName: pl}
	rom.PlaylistOrder = []string{playlistName}
	return nil
}
