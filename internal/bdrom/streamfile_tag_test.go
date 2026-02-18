package bdrom

import (
	"testing"

	"github.com/autobrr/go-bdinfo/internal/stream"
)

func TestUpdateStreamBitrate_DiagnosticsTagMayBeEmpty(t *testing.T) {
	const pid = uint16(0x1011)
	sf := &StreamFile{
		Streams:           map[uint16]stream.Info{},
		StreamDiagnostics: map[uint16][]StreamDiagnostics{},
	}
	sf.Streams[pid] = &stream.VideoStream{Stream: stream.Stream{PID: pid, StreamType: stream.StreamTypeAVCVideo}}

	st := &streamState{
		windowBytes:        1234,
		windowPackets:      7,
		collectDiagnostics: true,
		// streamTag intentionally empty (official BDInfo uses null).
	}

	sf.updateStreamBitrate(nil, nil, pid, 90000, 90000, st)

	diags := sf.StreamDiagnostics[pid]
	if len(diags) != 1 {
		t.Fatalf("expected 1 diagnostic entry, got %d", len(diags))
	}
	if diags[0].Tag != "" {
		t.Fatalf("expected empty tag, got %q", diags[0].Tag)
	}
	// Tag parse state resets per transfer.
	if st.streamTag != "" || st.tagParse != 0 || st.avcAUDParse != 0 || st.mpeg2PictureParse != 0 ||
		st.vc1FrameHeaderParse != 0 || st.vc1SeqHeaderParse != 0 || st.vc1IsInterlaced {
		t.Fatalf("expected tag parse state reset; got tag=%q parse=%d aud=%d mpeg2=%d vc1Frame=%d vc1Seq=%d vc1Interlaced=%v",
			st.streamTag, st.tagParse, st.avcAUDParse, st.mpeg2PictureParse, st.vc1FrameHeaderParse, st.vc1SeqHeaderParse, st.vc1IsInterlaced)
	}
}
