package udf

import (
	"bytes"
	"io"
	"os"
	"testing"
)

func TestDecodeString_UCS2BE(t *testing.T) {
	r := &Reader{}

	// compID=16 + UCS-2BE bytes for "BRITNEY_SPEARS" + terminator
	data := []byte{
		16,
		0x00, 'B',
		0x00, 'R',
		0x00, 'I',
		0x00, 'T',
		0x00, 'N',
		0x00, 'E',
		0x00, 'Y',
		0x00, '_',
		0x00, 'S',
		0x00, 'P',
		0x00, 'E',
		0x00, 'A',
		0x00, 'R',
		0x00, 'S',
		0x00, 0x00,
	}

	if got, want := r.decodeString(data), "BRITNEY_SPEARS"; got != want {
		t.Fatalf("decodeString(UCS2)=%q want %q", got, want)
	}
}

func TestDecodeString_8BitStopsAtNUL(t *testing.T) {
	r := &Reader{}
	if got, want := r.decodeString([]byte{8, 'A', 'B', 0, 'C'}), "AB"; got != want {
		t.Fatalf("decodeString(8bit)=%q want %q", got, want)
	}
}

func TestParsePartitionMaps_MetadataPartition(t *testing.T) {
	// Partition map table bytes from a UDF 2.50+ BD-ROM (metadata partition map).
	pm := []byte{
		0x01, 0x06, 0x01, 0x00, 0x00, 0x00, // type 1, len 6, volseq=1, part=0
		0x02, 0x40, 0x00, 0x00, // type 2, len 64, reserved
		0x00, // EntityID flags
		'*', 'U', 'D', 'F', ' ', 'M', 'e', 't', 'a', 'd', 'a', 't', 'a', ' ', 'P', 'a', 'r', 't', 'i', 't', 'i', 'o', 'n',
		0x50, 0x02, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, // suffix (opaque)
		// extent_ad(len=1, loc=0) for metadata file ICB location (common BD-ROM layout)
		0x01, 0x00, 0x00, 0x00,
		0x00, 0x00, 0x00, 0x00,
		// remaining fields (not used by go-bdinfo currently)
		0x3f, 0xca, 0xb9, 0x00,
		0xff, 0xff, 0xff, 0xff,
		0x20, 0x00, 0x00, 0x00,
		0x20, 0x00, 0x01, 0x00,
		0x00, 0x00, 0x00, 0x00,
	}

	r := &Reader{}
	if err := r.parsePartitionMaps(pm, 2); err != nil {
		t.Fatalf("parsePartitionMaps err: %v", err)
	}
	if got := len(r.partitionMaps); got != 2 {
		t.Fatalf("partitionMaps len=%d want 2", got)
	}
	if !r.partitionMaps[1].isMetadata {
		t.Fatalf("partitionMaps[1].isMetadata=false want true")
	}
	if r.metadataFileICB == nil {
		t.Fatalf("metadataFileICB=nil want non-nil")
	}
	if got, want := r.metadataFileICB.ExtentLocation.LogicalBlockNumber, uint32(0); got != want {
		t.Fatalf("metadataFileICB lbn=%d want %d", got, want)
	}
	if got, want := r.metadataFileICB.ExtentLocation.PartitionReferenceNumber, uint16(0); got != want {
		t.Fatalf("metadataFileICB pref=%d want %d", got, want)
	}
}

func TestExtentReader_ReadsAcrossExtents(t *testing.T) {
	f, err := os.CreateTemp(t.TempDir(), "udf-extents-*")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()

	a := bytes.Repeat([]byte("A"), 1024)
	b := bytes.Repeat([]byte("B"), 1024)

	if _, err := f.WriteAt(a, 0); err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteAt(b, 4096); err != nil {
		t.Fatal(err)
	}

	r := &Reader{file: f}
	er := &extentReader{
		reader: r,
		extents: []extent{
			{fileStart: 0, fileEnd: 1024, physOff: 0},
			{fileStart: 1024, fileEnd: 2048, physOff: 4096},
		},
		size: 2048,
	}

	got, err := io.ReadAll(er)
	if err != nil {
		t.Fatalf("ReadAll err: %v", err)
	}
	want := append(a, b...)
	if !bytes.Equal(got, want) {
		t.Fatalf("data mismatch: got len=%d want len=%d", len(got), len(want))
	}
}
