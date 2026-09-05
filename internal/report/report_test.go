package report

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/autobrr/go-bdinfo/internal/bdrom"
	"github.com/autobrr/go-bdinfo/internal/settings"
	"github.com/autobrr/go-bdinfo/internal/stream"
)

func TestWriteReport_StreamDiagnosticsHiddenStreamsLast(t *testing.T) {
	tmpDir := t.TempDir()
	outPath := filepath.Join(tmpDir, "out.bdinfo")
	cfg := settings.Default(tmpDir)
	cfg.GenerateTextSummary = false

	bd, playlists := newTestDisc(cfg)

	if _, err := WriteReport(outPath, bd, playlists, bdrom.ScanResult{}, cfg); err != nil {
		t.Fatalf("WriteReport() error = %v", err)
	}

	reportData, err := os.ReadFile(outPath)
	if err != nil {
		t.Fatalf("read report: %v", err)
	}
	out := string(reportData)

	iPrimary := strings.Index(out, "4113 (0x1011)")
	iAudio := strings.Index(out, "4352 (0x1100)")
	iGraphics := strings.Index(out, "4768 (0x12A0)")
	iHidden := strings.Index(out, "4117 (0x1015)")
	if iPrimary == -1 || iAudio == -1 || iGraphics == -1 || iHidden == -1 {
		t.Fatalf("missing stream diagnostics rows in report")
	}
	if !(iPrimary < iAudio && iAudio < iGraphics && iGraphics < iHidden) {
		t.Fatalf("unexpected diagnostics ordering: primary=%d audio=%d graphics=%d hidden=%d", iPrimary, iAudio, iGraphics, iHidden)
	}
}

func TestWriteReport_ReportFileNameExtensionHandling(t *testing.T) {
	tmpDir := t.TempDir()
	bd := &bdrom.BDROM{
		VolumeLabel: "TEST_DISC",
		Size:        123456789,
	}

	t.Run("preserves custom extension", func(t *testing.T) {
		cfg := settings.Default(tmpDir)
		cfg.ReportFileName = filepath.Join(tmpDir, "report.log")

		reportPath, err := WriteReport("", bd, nil, bdrom.ScanResult{}, cfg)
		if err != nil {
			t.Fatalf("WriteReport() error = %v", err)
		}
		if reportPath != cfg.ReportFileName {
			t.Fatalf("report path mismatch: got %q want %q", reportPath, cfg.ReportFileName)
		}
		if _, err := os.Stat(cfg.ReportFileName); err != nil {
			t.Fatalf("expected report file to exist at custom extension path: %v", err)
		}
	})

	t.Run("defaults to txt when extension missing", func(t *testing.T) {
		cfg := settings.Default(tmpDir)
		cfg.ReportFileName = filepath.Join(tmpDir, "report")
		expected := cfg.ReportFileName + ".txt"

		reportPath, err := WriteReport("", bd, nil, bdrom.ScanResult{}, cfg)
		if err != nil {
			t.Fatalf("WriteReport() error = %v", err)
		}
		if reportPath != expected {
			t.Fatalf("report path mismatch: got %q want %q", reportPath, expected)
		}
		if _, err := os.Stat(expected); err != nil {
			t.Fatalf("expected report file to exist at default txt path: %v", err)
		}
	})
}

// newTestDisc builds one UHD playlist with a hidden video stream, so the
// report has every block: forums paste, diagnostics, quick summary.
func newTestDisc(cfg settings.Settings) (*bdrom.BDROM, []*bdrom.PlaylistFile) {
	primaryVideo := &stream.VideoStream{
		Stream: stream.Stream{
			PID:          0x1011,
			StreamType:   stream.StreamTypeHEVCVideo,
			PayloadBytes: 1_000_000,
			PacketCount:  10_000,
		},
		Height:        2160,
		FrameRateEnum: 24000,
		FrameRateDen:  1001,
		AspectRatio:   stream.Aspect169,
	}
	hiddenVideo := &stream.VideoStream{
		Stream: stream.Stream{
			PID:          0x1015,
			StreamType:   stream.StreamTypeHEVCVideo,
			PayloadBytes: 10_000,
			PacketCount:  100,
		},
		Height:        1080,
		FrameRateEnum: 24000,
		FrameRateDen:  1001,
		AspectRatio:   stream.Aspect169,
	}
	hiddenPlaylistVideo := hiddenVideo.Clone().(*stream.VideoStream)
	hiddenPlaylistVideo.IsHidden = true
	audio := &stream.AudioStream{
		Stream: stream.Stream{
			PID:          0x1100,
			StreamType:   stream.StreamTypeLPCMAudio,
			PayloadBytes: 250_000,
			PacketCount:  1_000,
		},
		SampleRate:   48000,
		ChannelCount: 1,
	}
	audio.SetLanguageCode("eng")
	graphics := &stream.GraphicsStream{
		Stream: stream.Stream{
			PID:          0x12A0,
			StreamType:   stream.StreamTypePresentationGraphics,
			PayloadBytes: 50_000,
			PacketCount:  500,
		},
	}
	graphics.SetLanguageCode("eng")

	streamFile := &bdrom.StreamFile{
		Name:   "00007.M2TS",
		Length: 10.0,
		Streams: map[uint16]stream.Info{
			0x1011: primaryVideo,
			0x1015: hiddenVideo,
			0x1100: audio,
			0x12A0: graphics,
		},
		StreamOrder: []uint16{0x1011, 0x1100, 0x12A0, 0x1015},
	}
	playlist := &bdrom.PlaylistFile{
		Name:            "00001.MPLS",
		Settings:        cfg,
		IsInitialized:   true,
		HasHiddenTracks: true,
		Streams: map[uint16]stream.Info{
			0x1011: primaryVideo,
			0x1015: hiddenPlaylistVideo,
			0x1100: audio,
			0x12A0: graphics,
		},
		// Length must clear the short-playlist threshold (20s) so the playlist is
		// valid; the default now filters short/looping playlists like upstream.
		StreamClips: []*bdrom.StreamClip{
			{
				Settings:    cfg,
				Name:        "00007.M2TS",
				Length:      30.0,
				PacketCount: 34_800,
				StreamFile:  streamFile,
			},
		},
		VideoStreams:    []*stream.VideoStream{primaryVideo, hiddenPlaylistVideo},
		AudioStreams:    []*stream.AudioStream{audio},
		GraphicsStreams: []*stream.GraphicsStream{graphics},
		SortedStreams:   []stream.Info{primaryVideo, hiddenPlaylistVideo, audio, graphics},
	}
	bd := &bdrom.BDROM{
		VolumeLabel: "TEST_DISC",
		DiscTitle:   "TEST_DISC",
		Size:        123456789,
		IsUHD:       true,
	}

	return bd, []*bdrom.PlaylistFile{playlist}
}

func TestRenderReport_BlocksMatchCLIOutput(t *testing.T) {
	render := func(mutate ...func(*settings.Settings)) Output {
		cfg := settings.Default(t.TempDir())
		for _, m := range mutate {
			m(&cfg)
		}
		bd, playlists := newTestDisc(cfg)
		_, out, err := RenderReport("", bd, playlists, bdrom.ScanResult{}, cfg)
		if err != nil {
			t.Fatalf("RenderReport() error = %v", err)
		}
		return out
	}

	full := render()
	summaryOnly := render(func(s *settings.Settings) { s.SummaryOnly = true })
	forumsOnly := render(func(s *settings.Settings) { s.ForumsOnly = true })

	if !strings.HasPrefix(full.QuickSummary, "QUICK SUMMARY:\n") || !strings.Contains(full.QuickSummary, "Video: ") {
		t.Fatalf("QuickSummary missing content:\n%s", full.QuickSummary)
	}
	if !strings.HasPrefix(full.ForumsBlock, "<--- BEGIN FORUMS PASTE --->\n") || !strings.HasSuffix(full.ForumsBlock, "<---- END FORUMS PASTE ---->\n") {
		t.Fatalf("ForumsBlock missing markers:\n%s", full.ForumsBlock)
	}
	if !strings.HasPrefix(full.Report, "Disc Title:") || !strings.Contains(full.Report, strings.TrimSuffix(full.ForumsBlock, "\n")) {
		t.Fatalf("full Report should contain the forums block:\n%s", full.Report)
	}

	// Report equals the block the CLI prints for that flag.
	if summaryOnly.Report != full.QuickSummary {
		t.Fatalf("SummaryOnly Report != QuickSummary:\n%s\n---\n%s", summaryOnly.Report, full.QuickSummary)
	}
	if forumsOnly.Report != full.ForumsBlock {
		t.Fatalf("ForumsOnly Report != ForumsBlock:\n%s\n---\n%s", forumsOnly.Report, full.ForumsBlock)
	}

	// Both fields are filled the same way whichever trim flag is set.
	if summaryOnly.QuickSummary != full.QuickSummary || forumsOnly.QuickSummary != full.QuickSummary {
		t.Errorf("QuickSummary differs from full render when a trim flag is set")
	}
	if summaryOnly.ForumsBlock != full.ForumsBlock || forumsOnly.ForumsBlock != full.ForumsBlock {
		t.Errorf("ForumsBlock differs from full render when a trim flag is set")
	}

	noSummary := render(func(s *settings.Settings) { s.GenerateTextSummary = false })
	if noSummary.QuickSummary != "" {
		t.Fatalf("QuickSummary should be empty when GenerateTextSummary is off, got:\n%s", noSummary.QuickSummary)
	}
	if noSummary.ForumsBlock != full.ForumsBlock {
		t.Fatalf("ForumsBlock should not depend on GenerateTextSummary")
	}
}
