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
		HasHiddenTracks: true,
		Streams: map[uint16]stream.Info{
			0x1011: primaryVideo,
			0x1015: hiddenPlaylistVideo,
			0x1100: audio,
			0x12A0: graphics,
		},
		StreamClips: []*bdrom.StreamClip{
			{
				Settings:    cfg,
				Name:        "00007.M2TS",
				Length:      10.0,
				PacketCount: 11_600,
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

	if _, err := WriteReport(outPath, bd, []*bdrom.PlaylistFile{playlist}, bdrom.ScanResult{}, cfg); err != nil {
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
