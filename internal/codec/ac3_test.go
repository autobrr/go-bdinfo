package codec

import (
	"strings"
	"testing"

	"github.com/autobrr/go-bdinfo/internal/stream"
)

type testBitWriter struct {
	data []byte
	bit  int
}

func (w *testBitWriter) write(value uint64, bits int) {
	for i := bits - 1; i >= 0; i-- {
		if w.bit%8 == 0 {
			w.data = append(w.data, 0)
		}
		if ((value >> uint(i)) & 1) != 0 {
			w.data[len(w.data)-1] |= 1 << uint(7-(w.bit%8))
		}
		w.bit++
	}
}

func (w *testBitWriter) pad(size int) []byte {
	if len(w.data) > size {
		return w.data
	}
	return append(w.data, make([]byte, size-len(w.data))...)
}

func testAC3PlusCoreFrame() []byte {
	var w testBitWriter
	w.write(0x0b77, 16)
	w.write(0, 16) // crc1
	w.write(0, 2)  // 48 kHz
	w.write(34, 6) // 576 kbps
	w.write(6, 5)  // bsid
	w.write(0, 3)  // bsmod
	w.write(7, 3)  // 3/2
	w.write(0, 2)  // cmixlev
	w.write(0, 2)  // surmixlev
	w.write(1, 1)  // lfeon
	w.write(25, 5) // dialnorm
	w.write(0, 1)  // compre
	w.write(0, 1)  // langcode
	w.write(0, 1)  // audprodie
	w.write(0, 2)  // copyright/original
	w.write(0, 1)  // xbsi1e
	w.write(0, 1)  // xbsi2e
	return w.pad(2304)
}

func testAC3PlusDependentJOCFrame() []byte {
	var w testBitWriter
	w.write(0x0b77, 16)
	w.write(1, 2)       // dependent stream
	w.write(0, 3)       // substreamid
	w.write(1151, 11)   // 2304 bytes
	w.write(0, 2)       // 48 kHz
	w.write(3, 2)       // six blocks in this parser's BDInfo-compatible mapping
	w.write(7, 3)       // 3/2
	w.write(1, 1)       // lfeon
	w.write(16, 5)      // bsid
	w.write(25, 5)      // dialnorm
	w.write(0, 1)       // compre
	w.write(1, 1)       // chanmape
	w.write(0x0010, 16) // Tfl/Tfr

	w.write(0x5838, 16) // emdf sync
	w.write(8, 16)      // emdf_container_size
	w.write(0, 2)       // emdf_version
	w.write(0, 3)
	w.write(0, 5)  // first payload id
	w.write(14, 5) // JOC payload id
	writeTestEmdfPayloadConfig(&w)
	w.write(0, 8) // payload size
	w.write(0, 1) // skipped payload bit
	writeTestEmdfPayloadConfig(&w)
	w.write(0, 12)
	w.write(1, 6) // joc_num_objects_bits
	return w.pad(2304)
}

func testAC3PlusDependentFrameNoJOC() []byte {
	var w testBitWriter
	w.write(0x0b77, 16)
	w.write(1, 2)       // dependent stream
	w.write(0, 3)       // substreamid
	w.write(1151, 11)   // 2304 bytes
	w.write(0, 2)       // 48 kHz
	w.write(3, 2)       // six blocks in this parser's BDInfo-compatible mapping
	w.write(7, 3)       // 3/2
	w.write(1, 1)       // lfeon
	w.write(16, 5)      // bsid
	w.write(25, 5)      // dialnorm
	w.write(0, 1)       // compre
	w.write(1, 1)       // chanmape
	w.write(0x0010, 16) // Tfl/Tfr
	return w.pad(2304)
}

func writeTestEmdfPayloadConfig(w *testBitWriter) {
	w.write(0, 1) // sample_offsete
	w.write(0, 1) // duratione
	w.write(0, 1) // groupide
	w.write(0, 1) // codec-specific reserved flag
	w.write(0, 1) // discard_unknown_payload
	w.write(0, 1)
	w.write(0, 1) // payload_frame_aligned
}

func TestScanAC3_AC3PlusAtmosDependentFrame(t *testing.T) {
	a := &stream.AudioStream{Stream: stream.Stream{StreamType: stream.StreamTypeAC3PlusAudio}}
	data := append(testAC3PlusCoreFrame(), testAC3PlusDependentJOCFrame()...)

	ScanAC3(a, data)

	if !a.IsInitialized {
		t.Fatal("stream not initialized")
	}
	if !a.HasExtensions {
		t.Fatal("expected Atmos extension")
	}
	if a.ChannelLayoutText != "L R C LFE Ls Rs Tfl Tfr" {
		t.Fatalf("channel layout got %q want Tfl/Tfr height layout", a.ChannelLayoutText)
	}
	if a.ChannelDescription() != "5.1.2" {
		t.Fatalf("channel description got %q want 5.1.2", a.ChannelDescription())
	}
	if a.BitRate != 1152000 {
		t.Fatalf("bitrate got %d want 1152000", a.BitRate)
	}
	if a.CoreStream == nil {
		t.Fatal("expected embedded AC3 core")
	}
	if a.CoreStream.StreamType != stream.StreamTypeAC3Audio {
		t.Fatalf("core stream type got %v want AC3", a.CoreStream.StreamType)
	}
	desc := a.Description()
	if !strings.Contains(desc, "AC3 Embedded: 5.1 / 48 kHz /   576 kbps / DN -25dB") {
		t.Fatalf("description missing embedded core: %q", desc)
	}
}

func TestScanAC3_FrameBoundaryPreventsTrailingJOCDetection(t *testing.T) {
	a := &stream.AudioStream{Stream: stream.Stream{StreamType: stream.StreamTypeAC3PlusAudio}}
	ScanAC3(a, testAC3PlusCoreFrame())

	trailingJOC := testAC3PlusDependentJOCFrame()
	data := append(testAC3PlusDependentFrameNoJOC(), trailingJOC...)
	frameSize, ok := scanAC3Frame(a, data)

	if !ok {
		t.Fatal("expected dependent frame to parse")
	}
	if frameSize != 2304 {
		t.Fatalf("frame size got %d want 2304", frameSize)
	}
	if a.HasExtensions {
		t.Fatal("unexpected Atmos extension from trailing frame bytes")
	}
}

func TestScanAC3_RejectsTruncatedFrame(t *testing.T) {
	a := &stream.AudioStream{Stream: stream.Stream{StreamType: stream.StreamTypeAC3PlusAudio}}
	data := testAC3PlusCoreFrame()[:128]

	if frameSize, ok := scanAC3Frame(a, data); ok || frameSize != 0 {
		t.Fatalf("scanAC3Frame truncated frame got size=%d ok=%v", frameSize, ok)
	}
}
