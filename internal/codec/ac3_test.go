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

func testAC3PlusDependentFrame(substreamID uint64, chanmap uint64, joc bool) []byte {
	var w testBitWriter
	w.write(0x0b77, 16)
	w.write(1, 2)           // dependent stream
	w.write(substreamID, 3) // substreamid
	w.write(1151, 11)       // 2304 bytes
	w.write(0, 2)           // 48 kHz
	w.write(3, 2)           // six blocks in this parser's BDInfo-compatible mapping
	w.write(7, 3)           // 3/2
	w.write(1, 1)           // lfeon
	w.write(16, 5)          // bsid
	w.write(25, 5)          // dialnorm
	w.write(0, 1)           // compre
	w.write(1, 1)           // chanmape
	w.write(chanmap, 16)

	if joc {
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
	}
	return w.pad(2304)
}

func testAC3PlusDependentJOCFrame() []byte {
	return testAC3PlusDependentFrame(0, 0x0010, true) // Tfl/Tfr + JOC
}

func testAC3PlusIndependentFrame() []byte {
	var w testBitWriter
	w.write(0x0b77, 16)
	w.write(0, 2)     // independent stream
	w.write(0, 3)     // substreamid
	w.write(1151, 11) // 2304 bytes
	w.write(0, 2)     // 48 kHz
	w.write(3, 2)     // six blocks in this parser's BDInfo-compatible mapping
	w.write(7, 3)     // 3/2
	w.write(1, 1)     // lfeon
	w.write(16, 5)    // bsid
	w.write(25, 5)    // dialnorm
	w.write(0, 1)     // compre
	return w.pad(2304)
}

func testAC3PlusDependentFrameNoJOC() []byte {
	return testAC3PlusDependentFrame(0, 0x0010, false) // Tfl/Tfr, no JOC
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

// Real DD+ Atmos frame order: the independent frame is bsid-16 (not a bsid-6 AC-3
// core), so it initializes the stream on the first parse; the dependent JOC frame
// that follows must still be parsed or Atmos is silently dropped.
func TestScanAC3_AC3PlusAtmosRealFrameOrder(t *testing.T) {
	a := &stream.AudioStream{Stream: stream.Stream{StreamType: stream.StreamTypeAC3PlusAudio}}
	data := append(testAC3PlusIndependentFrame(), testAC3PlusDependentJOCFrame()...)

	ScanAC3(a, data)

	if !a.IsInitialized {
		t.Fatal("stream not initialized")
	}
	if !a.HasExtensions {
		t.Fatal("expected Atmos extension from dependent JOC frame")
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
		t.Fatal("expected embedded core from independent frame")
	}
}

// A plain DD+ stream (independent frames only, no dependent substream) must stop at
// the first frame exactly as before: no DialNorm from a second frame, no core.
func TestScanAC3_AC3PlusIndependentOnlyStopsAtFirstFrame(t *testing.T) {
	a := &stream.AudioStream{Stream: stream.Stream{StreamType: stream.StreamTypeAC3PlusAudio}}
	data := append(testAC3PlusIndependentFrame(), testAC3PlusIndependentFrame()...)

	ScanAC3(a, data)

	if !a.IsInitialized {
		t.Fatal("stream not initialized")
	}
	if a.DialNorm != 0 {
		t.Fatalf("DialNorm got %d want 0 (second independent frame must not be parsed)", a.DialNorm)
	}
	if a.CoreStream != nil {
		t.Fatal("unexpected core stream for independent-only DD+")
	}
	if a.ChannelDescription() != "5.1" {
		t.Fatalf("channel description got %q want 5.1", a.ChannelDescription())
	}
}

// E-AC-3 allows up to eight dependent substreams per independent frame; JOC and
// channel extensions may live in a later dependent, so the walk must merge every
// consecutive dependent frame, not stop at the first one.
func TestScanAC3_AC3PlusMultipleDependentSubstreams(t *testing.T) {
	a := &stream.AudioStream{Stream: stream.Stream{StreamType: stream.StreamTypeAC3PlusAudio}}
	data := append(testAC3PlusIndependentFrame(), testAC3PlusDependentFrame(0, 0x0200, false)...) // Lb/Rb
	data = append(data, testAC3PlusDependentFrame(1, 0x0010, true)...)                            // Tfl/Tfr + JOC

	ScanAC3(a, data)

	if !a.HasExtensions {
		t.Fatal("expected Atmos extension from second dependent frame")
	}
	if a.ChannelLayoutText != "L R C LFE Ls Rs Lb Rb Tfl Tfr" {
		t.Fatalf("channel layout got %q want merged layout from both dependents", a.ChannelLayoutText)
	}
	if a.ChannelCount != 9 {
		t.Fatalf("channel count got %d want 9 (5 core + 2 + 2)", a.ChannelCount)
	}
	if a.CoreStream == nil || a.CoreStream.ChannelCount != 5 {
		t.Fatalf("core stream must be the independent-frame snapshot, got %+v", a.CoreStream)
	}
}

// Dependent frames with no preceding independent frame (buffer starts mid
// access-unit) carry extension data that is meaningless on its own: they must be
// skipped without initializing the stream or fabricating an embedded core.
func TestScanAC3_DependentFrameFirstDoesNotFabricateCore(t *testing.T) {
	a := &stream.AudioStream{Stream: stream.Stream{StreamType: stream.StreamTypeAC3PlusAudio}}
	data := append(testAC3PlusDependentJOCFrame(), testAC3PlusDependentFrame(1, 0x0010, true)...)
	ScanAC3(a, data)

	if a.CoreStream != nil {
		t.Fatal("unexpected core stream cloned from uninitialized state")
	}
	if a.IsInitialized {
		t.Fatal("leading dependent frames must not initialize the stream")
	}
	if a.HasExtensions {
		t.Fatal("unexpected Atmos extension from orphan dependent frames")
	}
}

// A mid-access-unit start must recover: skip the leading dependent, then parse
// the independent frame and its dependents normally.
func TestScanAC3_DependentFrameFirstRecoversAtIndependent(t *testing.T) {
	a := &stream.AudioStream{Stream: stream.Stream{StreamType: stream.StreamTypeAC3PlusAudio}}
	data := append(testAC3PlusDependentJOCFrame(), testAC3PlusIndependentFrame()...)
	data = append(data, testAC3PlusDependentJOCFrame()...)

	ScanAC3(a, data)

	if !a.IsInitialized {
		t.Fatal("stream not initialized")
	}
	if !a.HasExtensions {
		t.Fatal("expected Atmos extension from the complete access unit")
	}
	if a.ChannelDescription() != "5.1.2" {
		t.Fatalf("channel description got %q want 5.1.2", a.ChannelDescription())
	}
	if a.CoreStream == nil {
		t.Fatal("expected embedded core from independent frame")
	}
}

func TestScanAC3_FrameBoundaryPreventsTrailingJOCDetection(t *testing.T) {
	a := &stream.AudioStream{Stream: stream.Stream{StreamType: stream.StreamTypeAC3PlusAudio}}
	ScanAC3(a, testAC3PlusCoreFrame())

	trailingJOC := testAC3PlusDependentJOCFrame()
	data := append(testAC3PlusDependentFrameNoJOC(), trailingJOC...)
	frameSize, ok := scanAC3Frame(a, data, true)

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

func TestScanAC3_ShortEAC3FrameDoesNotPanic(t *testing.T) {
	a := &stream.AudioStream{Stream: stream.Stream{StreamType: stream.StreamTypeAC3PlusAudio}}
	// E-AC-3 header with frmsiz 0: claimed frame size (2 bytes) is shorter than
	// the header fields the parser reads. Seen as a false 0x0b77 sync in payload.
	ScanAC3(a, []byte{0x0b, 0x77, 0x00, 0x00, 0x00, 0x58, 0x00})

	if a.IsInitialized {
		t.Fatal("garbage short frame must not initialize stream")
	}
}

func TestScanAC3_RejectsTruncatedFrame(t *testing.T) {
	a := &stream.AudioStream{Stream: stream.Stream{StreamType: stream.StreamTypeAC3PlusAudio}}
	data := testAC3PlusCoreFrame()[:128]

	if frameSize, ok := scanAC3Frame(a, data, false); ok || frameSize != 0 {
		t.Fatalf("scanAC3Frame truncated frame got size=%d ok=%v", frameSize, ok)
	}
}
