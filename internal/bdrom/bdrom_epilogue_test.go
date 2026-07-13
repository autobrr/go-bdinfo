package bdrom

import (
	"testing"

	"github.com/autobrr/go-bdinfo/internal/stream"
)

// Builds a 3D BDROM where the first playlist makes the disc 50Hz and the second
// carries the AVC+MVC pair whose BaseView the scan epilogue assigns.
func testEpilogueBDROM() *BDROM {
	fiftyHz := &stream.VideoStream{}
	fiftyHz.StreamType = stream.StreamTypeAVCVideo
	fiftyHz.SetFrameRate(stream.FrameRate25)
	plA := &PlaylistFile{Name: "00001.MPLS", VideoStreams: []*stream.VideoStream{fiftyHz}}

	avc := &stream.VideoStream{}
	avc.StreamType = stream.StreamTypeAVCVideo
	mvc := &stream.VideoStream{}
	mvc.StreamType = stream.StreamTypeMVCVideo
	plB := &PlaylistFile{Name: "00002.MPLS", VideoStreams: []*stream.VideoStream{avc, mvc}}

	return &BDROM{
		Is3D:             true,
		PlaylistFiles:    map[string]*PlaylistFile{plA.Name: plA, plB.Name: plB},
		PlaylistOrder:    []string{plA.Name, plB.Name},
		StreamClipFiles:  map[string]*StreamClipFile{},
		StreamFiles:      map[string]*StreamFile{},
		InterleavedFiles: map[string]*InterleavedFile{},
	}
}

// The metadata scan and the full scan must apply the identical 50Hz/BaseView
// epilogue (upstream BDInfo semantics: `if Is50Hz continue`). The two epilogues
// were copy-pasted and had already drifted.
func TestScanEpilogue_MetadataMatchesFullScan(t *testing.T) {
	metaROM := testEpilogueBDROM()
	fullROM := testEpilogueBDROM()

	metaROM.ScanMetadata()
	fullROM.Scan()

	if metaROM.Is50Hz != fullROM.Is50Hz {
		t.Fatalf("Is50Hz drift: metadata=%v full=%v", metaROM.Is50Hz, fullROM.Is50Hz)
	}
	for i := range 2 {
		meta := metaROM.PlaylistFiles["00002.MPLS"].VideoStreams[i].BaseView
		full := fullROM.PlaylistFiles["00002.MPLS"].VideoStreams[i].BaseView
		if (meta == nil) != (full == nil) {
			t.Fatalf("BaseView drift on stream %d: metadata=%v full=%v", i, meta, full)
		}
		if meta != nil && *meta != *full {
			t.Fatalf("BaseView value drift on stream %d: metadata=%v full=%v", i, *meta, *full)
		}
	}
}
