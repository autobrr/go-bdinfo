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
	// Two type1 maps precede the metadata map so the test can distinguish
	// "matched the type1 map for partition 2 at index 1" from "found no
	// match and defaulted pref to 0" (index 0 is a non-matching partition).
	pm := []byte{
		0x01, 0x06, 0x01, 0x00, 0x09, 0x00, // type 1, len 6, volseq=1, part=9 (non-matching)
		0x01, 0x06, 0x01, 0x00, 0x02, 0x00, // type 1, len 6, volseq=1, part=2 (matches metadata map)
		0x02, 0x40, // type 2, len 64
		0x00, 0x00, // reserved
		0x00, // EntityID flags
		'*', 'U', 'D', 'F', ' ', 'M', 'e', 't', 'a', 'd', 'a', 't', 'a', ' ', 'P', 'a', 'r', 't', 'i', 't', 'i', 'o', 'n',
		0x50, 0x02, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, // suffix (opaque)
		0x01, 0x00, // volume sequence number
		0x02, 0x00, // partition number
		// MetadataFileLocation is a uint32 LBN at offset 40 from the map start.
		0x78, 0x56, 0x34, 0x12,
		// remaining fields (not used by go-bdinfo currently)
		0x3f, 0xca, 0xb9, 0x00,
		0xff, 0xff, 0xff, 0xff,
		0x20, 0x00, 0x00, 0x00,
		0x20, 0x00, 0x01, 0x00,
		0x00, 0x00, 0x00, 0x00,
	}

	r := &Reader{}
	if err := r.parsePartitionMaps(pm, 3); err != nil {
		t.Fatalf("parsePartitionMaps err: %v", err)
	}
	if got := len(r.partitionMaps); got != 3 {
		t.Fatalf("partitionMaps len=%d want 3", got)
	}
	if !r.partitionMaps[2].isMetadata {
		t.Fatalf("partitionMaps[2].isMetadata=false want true")
	}
	if r.metadataFileICB == nil {
		t.Fatalf("metadataFileICB=nil want non-nil")
	}
	if got, want := r.metadataFileICB.ExtentLocation.LogicalBlockNumber, uint32(0x12345678); got != want {
		t.Fatalf("metadataFileICB lbn=%d want %d", got, want)
	}
	if got, want := r.metadataFileICB.ExtentLocation.PartitionReferenceNumber, uint16(1); got != want {
		t.Fatalf("metadataFileICB pref=%d want %d", got, want)
	}
}

func TestParsePartitionMaps_MetadataPartitionNoMatchingType1IsError(t *testing.T) {
	// Same metadata map as above (partition number 2) but with no type1
	// map at all — must error instead of silently defaulting pref to 0,
	// which previously could alias the metadata map itself and recurse.
	pm := []byte{
		0x02, 0x40, // type 2, len 64
		0x00, 0x00, // reserved
		0x00, // EntityID flags
		'*', 'U', 'D', 'F', ' ', 'M', 'e', 't', 'a', 'd', 'a', 't', 'a', ' ', 'P', 'a', 'r', 't', 'i', 't', 'i', 'o', 'n',
		0x50, 0x02, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, // suffix (opaque)
		0x01, 0x00, // volume sequence number
		0x02, 0x00, // partition number
		0x78, 0x56, 0x34, 0x12,
		0x3f, 0xca, 0xb9, 0x00,
		0xff, 0xff, 0xff, 0xff,
		0x20, 0x00, 0x00, 0x00,
		0x20, 0x00, 0x01, 0x00,
		0x00, 0x00, 0x00, 0x00,
	}

	r := &Reader{}
	if err := r.parsePartitionMaps(pm, 1); err == nil {
		t.Fatalf("parsePartitionMaps err=nil want error for unmatched metadata partition number")
	}
}

func TestFileSetDescriptorBlockUsesLogicalVolumePartitionReference(t *testing.T) {
	r := &Reader{
		partitionStart:            100,
		fileSetLocation:           77,
		fileSetPartitionReference: 1,
		partitionStarts:           map[uint16]uint32{5: 2000},
		partitionMaps:             []partitionMap{{kind: partitionMapType1, partitionNumber: 0}, {kind: partitionMapType1, partitionNumber: 5}},
	}

	got, err := r.fileSetDescriptorBlock()
	if err != nil {
		t.Fatalf("fileSetDescriptorBlock err: %v", err)
	}
	if want := uint32(2077); got != want {
		t.Fatalf("fileSetDescriptorBlock=%d want %d", got, want)
	}
}

func TestFileSetDescriptorBlockRoutesThroughMetadataPartition(t *testing.T) {
	// Canonical UDF 2.50 layout: FSD's partition reference points at the
	// type2 metadata map, whose file entry (pre-populated here to avoid
	// I/O) has one 2048-byte extent starting at partition block 4.
	r := &Reader{
		partitionStart:            100,
		fileSetLocation:           0,
		fileSetPartitionReference: 0,
		partitionMaps: []partitionMap{
			{kind: partitionMapType2, isMetadata: true, partitionNumber: 1},
			{kind: partitionMapType1, partitionNumber: 1},
		},
		metadataFileAllocDescs: []allocationDescriptor{
			{pref: 1, lbn: 4, length: 2048},
		},
	}

	got, err := r.fileSetDescriptorBlock()
	if err != nil {
		t.Fatalf("fileSetDescriptorBlock err: %v", err)
	}
	if want := uint32(104); got != want {
		t.Fatalf("fileSetDescriptorBlock=%d want %d", got, want)
	}
}

func TestDecodeLogicalVolumeContentsUseAsLongAD(t *testing.T) {
	var contentsUse [16]byte
	// long_ad: ExtentLength at 0:4, LBN at 4:8, partition reference at
	// 8:10, implementation use at 10:16.
	contentsUse[0] = 0x00
	contentsUse[1] = 0x08
	contentsUse[4] = 0x34
	contentsUse[5] = 0x12
	contentsUse[8] = 0x02

	lbn, pref, ok := decodeLogicalVolumeContentsUse(contentsUse)
	if !ok {
		t.Fatalf("decodeLogicalVolumeContentsUse ok=false want true")
	}
	if want := uint32(0x1234); lbn != want {
		t.Fatalf("lbn=%d want %d", lbn, want)
	}
	if want := uint16(2); pref != want {
		t.Fatalf("pref=%d want %d", pref, want)
	}
}

func TestDecodeLogicalVolumeContentsUse_AllZeroIsUnused(t *testing.T) {
	var contentsUse [16]byte
	_, _, ok := decodeLogicalVolumeContentsUse(contentsUse)
	if ok {
		t.Fatalf("decodeLogicalVolumeContentsUse ok=true want false for all-zero contentsUse")
	}
}

func TestDecodeLogicalVolumeContentsUse_ZeroExtentLengthIsUnused(t *testing.T) {
	var contentsUse [16]byte
	// ExtentLength (0:4) is zero but LBN (4:8) is non-zero: still unused
	// per ECMA-167 4/14.14.1.2 — ExtentLength governs, not LBN.
	contentsUse[4] = 0x34
	contentsUse[5] = 0x12
	_, _, ok := decodeLogicalVolumeContentsUse(contentsUse)
	if ok {
		t.Fatalf("decodeLogicalVolumeContentsUse ok=true want false for zero ExtentLength")
	}
}

func TestDecodeLogicalVolumeContentsUse_ZeroLBNIsValid(t *testing.T) {
	var contentsUse [16]byte
	// Canonical UDF 2.50 metadata-partition FSD: ExtentLength=2048,
	// LBN=0 (block 0 of the metadata partition), pref=1.
	contentsUse[0] = 0x00
	contentsUse[1] = 0x08
	contentsUse[8] = 0x01

	lbn, pref, ok := decodeLogicalVolumeContentsUse(contentsUse)
	if !ok {
		t.Fatalf("decodeLogicalVolumeContentsUse ok=false want true for LBN=0 with non-zero ExtentLength")
	}
	if lbn != 0 {
		t.Fatalf("lbn=%d want 0", lbn)
	}
	if want := uint16(1); pref != want {
		t.Fatalf("pref=%d want %d", pref, want)
	}
}

func TestReadEmbeddedDirectoryDataDecodesUCS2FileIdentifier(t *testing.T) {
	name := []byte{16, 0, 'B', 0, 'D', 0, 'M', 0, 'V'}
	data := make([]byte, 48)
	data[18] = FileCharDirectory
	data[19] = byte(len(name))
	copy(data[38:], name)

	dir := &Directory{reader: &Reader{}}
	if err := dir.readEmbeddedDirectoryData(data); err != nil {
		t.Fatalf("readEmbeddedDirectoryData err: %v", err)
	}
	if len(dir.entries) != 1 {
		t.Fatalf("entries len=%d want 1", len(dir.entries))
	}
	if got, want := dir.getFileName(dir.entries[0]), "BDMV"; got != want {
		t.Fatalf("dir name=%q want %q", got, want)
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
