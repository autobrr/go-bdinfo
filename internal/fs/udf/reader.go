package udf

import (
	"encoding/binary"
	"fmt"
	"io"
	"os"
	"strings"
)

// Reader provides UDF file system reading capabilities
type Reader struct {
	file            *os.File
	volumeLabel     string
	blockSize       uint32
	partitionStart  uint32
	partitionSize   uint32
	partitionStarts map[uint16]uint32
	partitionMaps   []partitionMap
	rootICB         LongAD
	fileSetDesc     *FileSetDescriptor
	fileSetLocation uint32

	metadataFileICB        *LongAD
	metadataFileAllocDescs []allocationDescriptor
}

type partitionMapKind uint8

const (
	partitionMapType1 partitionMapKind = 1
	partitionMapType2 partitionMapKind = 2
)

type partitionMap struct {
	kind partitionMapKind

	// Type 1 map.
	partitionNumber uint16

	// Type 2 metadata map ("UDF Metadata Partition").
	metadataICBLBN uint32 // file entry ICB LBN in partition ref 0
	isMetadata     bool
}

// NewReader creates a new UDF reader
func NewReader(path string) (*Reader, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("failed to open ISO file: %w", err)
	}

	reader := &Reader{
		file:            file,
		blockSize:       SectorSize,
		partitionStarts: make(map[uint16]uint32),
	}

	if err := reader.initialize(); err != nil {
		file.Close()
		return nil, err
	}

	return reader, nil
}

// Close closes the UDF reader
func (r *Reader) Close() error {
	if r.file != nil {
		return r.file.Close()
	}
	return nil
}

// GetVolumeLabel returns the volume label
func (r *Reader) GetVolumeLabel() string {
	return r.volumeLabel
}

// initialize reads UDF structures and prepares for file access
func (r *Reader) initialize() error {

	// Verify UDF volume
	if err := r.verifyVolume(); err != nil {
		return fmt.Errorf("not a valid UDF volume: %w", err)
	}

	// Find and read anchor volume descriptor
	anchor, err := r.findAnchorVolumeDescriptor()
	if err != nil {
		return fmt.Errorf("failed to find anchor volume descriptor: %w", err)
	}

	// Read volume descriptor sequence
	if err := r.readVolumeDescriptorSequence(anchor.MainVolumeDescriptorSequenceExtent); err != nil {
		return fmt.Errorf("failed to read volume descriptor sequence: %w", err)
	}

	// Now read the file set descriptor after we have partition info

	if r.fileSetLocation > 0 {
		location := r.partitionStart + r.fileSetLocation

		if _, err := r.file.Seek(int64(location)*int64(r.blockSize), io.SeekStart); err != nil {
			return fmt.Errorf("failed to seek to file set descriptor: %w", err)
		}

		var fsd FileSetDescriptor
		if err := r.readDescriptor(&fsd); err != nil {
			return fmt.Errorf("failed to read file set descriptor: %w", err)
		}

		// Check tag
		if fsd.DescriptorTag.TagIdentifier != TagFileSet {
			return fmt.Errorf("invalid file set descriptor tag: %d (expected %d) at location %d (partition start: %d, fileSetLocation: %d)",
				fsd.DescriptorTag.TagIdentifier, TagFileSet, location, r.partitionStart, r.fileSetLocation)
		}

		r.fileSetDesc = &fsd
		r.rootICB = fsd.RootDirectoryICB
	} else {
		return fmt.Errorf("file set location not determined")
	}

	return nil
}

// verifyVolume checks for UDF volume recognition sequence
func (r *Reader) verifyVolume() error {
	// Read VRS starting at sector 16
	if _, err := r.file.Seek(VRSOffset, io.SeekStart); err != nil {
		return err
	}

	foundNSR := false
	foundTerminator := false
	descriptors := []string{} // Track what we find for debugging

	for i := 0; i < 16 && !foundTerminator; i++ { // Check up to 16 sectors
		var vrs VolumeRecognitionDescriptor
		if err := binary.Read(r.file, binary.LittleEndian, &vrs); err != nil {
			// If we can't read a full descriptor, stop
			if err == io.EOF {
				break
			}
			return err
		}

		identifier := strings.TrimRight(string(vrs.StandardIdentifier[:]), "\x00")
		descriptors = append(descriptors, fmt.Sprintf("Sector %d: '%s' (raw: %x)", 16+i, identifier, vrs.StandardIdentifier))

		switch identifier {
		case StandardIDBEA01:
			// Beginning Extended Area
			continue
		case StandardIDNSR02, StandardIDNSR03:
			// NSR descriptor found
			foundNSR = true
		case StandardIDTEA01:
			// Terminating Extended Area
			foundTerminator = true
		case "":
			// Empty sector, stop searching
			foundTerminator = true
		default:
			// Unknown descriptor, could be end of VRS
			if !foundNSR {
				// If we haven't found NSR yet and hit unknown data, fail
				return fmt.Errorf("NSR descriptor not found in VRS. Found descriptors: %v", descriptors)
			}
			foundTerminator = true
		}
	}

	if !foundNSR {
		return fmt.Errorf("NSR descriptor not found. Scanned descriptors: %v", descriptors)
	}

	return nil
}

// findAnchorVolumeDescriptor locates the anchor volume descriptor
func (r *Reader) findAnchorVolumeDescriptor() (*AnchorVolumeDescriptorPointer, error) {
	// Try standard locations: sector 256, N-256, N, 512
	locations := []int64{256, 512}

	// Get file size to check end locations
	info, err := r.file.Stat()
	if err != nil {
		return nil, err
	}
	totalSectors := info.Size() / SectorSize
	locations = append(locations, totalSectors-256, totalSectors)

	for _, sector := range locations {
		if sector < 0 || sector*SectorSize >= info.Size() {
			continue
		}

		if _, err := r.file.Seek(sector*SectorSize, io.SeekStart); err != nil {
			continue
		}

		var tag Tag
		if err := binary.Read(r.file, binary.LittleEndian, &tag); err != nil {
			continue
		}

		if tag.TagIdentifier == TagAnchorVolume {
			// Read the rest of the anchor descriptor
			r.file.Seek(sector*SectorSize, io.SeekStart)
			anchor := &AnchorVolumeDescriptorPointer{}
			if err := r.readDescriptor(anchor); err != nil {
				continue
			}
			return anchor, nil
		}
	}

	return nil, fmt.Errorf("anchor volume descriptor not found")
}

// readVolumeDescriptorSequence reads the main volume descriptor sequence
func (r *Reader) readVolumeDescriptorSequence(extent ExtentAD) error {
	if _, err := r.file.Seek(int64(extent.Location)*SectorSize, io.SeekStart); err != nil {
		return err
	}

	bytesRead := uint32(0)
	for bytesRead < extent.Length {
		var tag Tag
		tagPos := r.getCurrentPosition()

		if err := binary.Read(r.file, binary.LittleEndian, &tag); err != nil {
			return err
		}

		// Seek back to read full descriptor
		r.file.Seek(tagPos, io.SeekStart)

		switch tag.TagIdentifier {
		case TagPrimaryVolume:
			var pvd PrimaryVolumeDescriptor
			if err := r.readDescriptor(&pvd); err != nil {
				return err
			}
			r.volumeLabel = r.decodeString(pvd.VolumeIdentifier[:])

		case TagPartition:
			var pd PartitionDescriptor
			if err := r.readDescriptor(&pd); err != nil {
				return err
			}
			r.partitionStarts[pd.PartitionNumber] = pd.PartitionStartingLocation
			// Keep legacy single-partition fields for callers that assume one partition.
			if r.partitionStart == 0 {
				r.partitionStart = pd.PartitionStartingLocation
				r.partitionSize = pd.PartitionLength
			}

		case TagLogicalVolume:
			var lvd LogicalVolumeDescriptor
			if err := r.readDescriptor(&lvd); err != nil {
				return err
			}
			if lvd.LogicalBlockSize != 0 {
				r.blockSize = lvd.LogicalBlockSize
			}
			if lvd.MapTableLength > 0 && lvd.NumberOfPartitionMaps > 0 {
				pm := make([]byte, lvd.MapTableLength)
				if _, err := io.ReadFull(r.file, pm); err != nil {
					return fmt.Errorf("failed to read partition map table: %w", err)
				}
				if err := r.parsePartitionMaps(pm, lvd.NumberOfPartitionMaps); err != nil {
					return fmt.Errorf("failed to parse partition maps: %w", err)
				}
			}
			// Extract root directory location from logical volume contents use
			// The first 8 bytes contain the file set descriptor location as ExtentAD
			_ = binary.LittleEndian.Uint32(lvd.LogicalVolumeContentsUse[0:4]) // fileSetLength
			fileSetLocation := binary.LittleEndian.Uint32(lvd.LogicalVolumeContentsUse[4:8])

			// Debug: check if LogicalVolumeContentsUse has data
			hasData := false
			for _, b := range lvd.LogicalVolumeContentsUse {
				if b != 0 {
					hasData = true
					break
				}
			}

			if !hasData || fileSetLocation == 0 {
				// LogicalVolumeContentsUse is empty or zero
				// Try common fallback locations for FileSet descriptor
				// Most Blu-ray discs put it at sector 32 of the partition
				fileSetLocation = 32
			}

			// We need to defer reading the file set descriptor until after we've processed
			// all volume descriptors (especially the partition descriptor)
			// For now, just store the location
			r.fileSetLocation = fileSetLocation

		case TagTerminating:
			// End of sequence
			return nil

		default:
			// Skip unknown descriptor
			// Move to next sector
		}

		// Always advance by one sector
		bytesRead += SectorSize
		r.file.Seek(int64(extent.Location+bytesRead/SectorSize)*int64(SectorSize), io.SeekStart)
	}

	return nil
}

// readDescriptor reads a descriptor with its tag
func (r *Reader) readDescriptor(desc any) error {
	return binary.Read(r.file, binary.LittleEndian, desc)
}

// getCurrentPosition returns current file position
func (r *Reader) getCurrentPosition() int64 {
	pos, _ := r.file.Seek(0, io.SeekCurrent)
	return pos
}

// decodeString decodes a dstring (UDF compressed unicode)
func (r *Reader) decodeString(data []byte) string {
	if len(data) == 0 {
		return ""
	}

	compType := data[0]
	switch compType {
	case 8:
		// 8-bit (typically Latin-1). Stop at first NUL.
		s := string(data[1:])
		if idx := strings.IndexByte(s, 0); idx >= 0 {
			s = s[:idx]
		}
		return strings.TrimRight(s, " ")
	case 16:
		// UCS-2 (OSTA CS0), big-endian code units. Stop at first 0x0000.
		b := data[1:]
		runes := make([]rune, 0, len(b)/2)
		for i := 0; i+1 < len(b); i += 2 {
			u := uint16(b[i])<<8 | uint16(b[i+1])
			if u == 0 {
				break
			}
			runes = append(runes, rune(u))
		}
		return strings.TrimRight(string(runes), " ")
	}

	return ""
}

func (r *Reader) BlockSize() uint32      { return r.blockSize }
func (r *Reader) PartitionStart() uint32 { return r.partitionStart }
func (r *Reader) FileSetLocation() uint32 {
	return r.fileSetLocation
}
func (r *Reader) RootICB() LongAD { return r.rootICB }

func (r *Reader) DebugPartitionMaps() []string {
	var out []string
	for i, pm := range r.partitionMaps {
		switch pm.kind {
		case partitionMapType1:
			out = append(out, fmt.Sprintf("%d:type1 partNum=%d", i, pm.partitionNumber))
		case partitionMapType2:
			if pm.isMetadata {
				out = append(out, fmt.Sprintf("%d:type2 metadata icbLBN=%d", i, pm.metadataICBLBN))
			} else {
				out = append(out, fmt.Sprintf("%d:type2", i))
			}
		default:
			out = append(out, fmt.Sprintf("%d:unknown", i))
		}
	}
	return out
}

const udfMetadataPartitionIdent = "UDF Metadata Partition"

func (r *Reader) parsePartitionMaps(pm []byte, n uint32) error {
	r.partitionMaps = nil
	r.metadataFileICB = nil
	r.metadataFileAllocDescs = nil

	off := 0
	for i := uint32(0); i < n; i++ {
		if off+2 > len(pm) {
			return fmt.Errorf("partition map %d: truncated header", i)
		}
		mtype := pm[off]
		mlen := int(pm[off+1])
		if mlen < 2 || off+mlen > len(pm) {
			return fmt.Errorf("partition map %d: invalid length %d", i, mlen)
		}

		switch partitionMapKind(mtype) {
		case partitionMapType1:
			// Type 1: type, len, volSeq (2), partitionNumber (2).
			if mlen < 6 {
				return fmt.Errorf("partition map %d: type1 too short: %d", i, mlen)
			}
			partNum := binary.LittleEndian.Uint16(pm[off+4 : off+6])
			r.partitionMaps = append(r.partitionMaps, partitionMap{
				kind:            partitionMapType1,
				partitionNumber: partNum,
			})

		case partitionMapType2:
			m := partitionMap{kind: partitionMapType2}
			if mlen >= 4+32 {
				ident := strings.TrimRight(string(pm[off+5:off+5+23]), "\x00")
				ident = strings.TrimPrefix(ident, "*")
				if ident == udfMetadataPartitionIdent {
					m.isMetadata = true
					// UDF Metadata Partition Map:
					// Common BD-ROM layout encodes the metadata file ICB location as extent_ad
					// (len=1, loc=<lbn>) at offset 36 from start of the map.
					if mlen >= 36+8 {
						extLen := binary.LittleEndian.Uint32(pm[off+36 : off+40])
						extLoc := binary.LittleEndian.Uint32(pm[off+40 : off+44])
						if extLen == 1 {
							m.metadataICBLBN = extLoc
						} else {
							// Fallback: interpret extLen as the LBN (seen on some images).
							m.metadataICBLBN = extLen
						}
					}
				}
			}
			r.partitionMaps = append(r.partitionMaps, m)

		default:
			r.partitionMaps = append(r.partitionMaps, partitionMap{})
		}

		off += mlen
	}

	for _, m := range r.partitionMaps {
		if m.kind == partitionMapType2 && m.isMetadata {
			icb := LongAD{
				ExtentLocation: LBAddr{
					LogicalBlockNumber:       m.metadataICBLBN,
					PartitionReferenceNumber: 0, // metadata file lives in main partition map
				},
			}
			r.metadataFileICB = &icb
			break
		}
	}

	if r.metadataFileICB != nil && r.file != nil {
		if _, err := r.metadataFileAllocationDescriptors(); err != nil {
			return err
		}
	}

	return nil
}

func (r *Reader) resolvePartitionBlock(partRef uint16, lbn uint32) (uint32, error) {
	return r.resolveLBAddr(LBAddr{
		LogicalBlockNumber:       lbn,
		PartitionReferenceNumber: partRef,
	})
}

func (r *Reader) resolveLBAddr(addr LBAddr) (uint32, error) {
	pref := int(addr.PartitionReferenceNumber)
	if pref >= 0 && pref < len(r.partitionMaps) {
		pm := r.partitionMaps[pref]
		switch pm.kind {
		case partitionMapType1:
			start := r.partitionStart
			if ps, ok := r.partitionStarts[pm.partitionNumber]; ok {
				start = ps
			}
			return start + addr.LogicalBlockNumber, nil
		case partitionMapType2:
			if pm.isMetadata {
				return r.resolveMetadataBlock(addr.LogicalBlockNumber)
			}
		}
	}

	// Fallback: treat as a single direct partition.
	return r.partitionStart + addr.LogicalBlockNumber, nil
}

func (r *Reader) resolveMetadataBlock(lbn uint32) (uint32, error) {
	allocs, err := r.metadataFileAllocationDescriptors()
	if err != nil {
		return 0, err
	}

	blockSize := r.blockSize
	if blockSize == 0 {
		blockSize = SectorSize
	}

	var fileBlockBase uint32
	for _, ad := range allocs {
		extentBytes := ad.length
		if extentBytes == 0 {
			continue
		}
		extentBlocks := extentBytes / blockSize
		if extentBytes%blockSize != 0 {
			extentBlocks++
		}

		if lbn < fileBlockBase+extentBlocks {
			within := lbn - fileBlockBase
			return r.resolvePartitionBlock(ad.pref, ad.lbn+within)
		}
		fileBlockBase += extentBlocks
	}

	return 0, fmt.Errorf("metadata block out of range: %d", lbn)
}

func (r *Reader) metadataFileAllocationDescriptors() ([]allocationDescriptor, error) {
	if r.metadataFileAllocDescs != nil {
		return r.metadataFileAllocDescs, nil
	}
	if r.metadataFileICB == nil {
		return nil, fmt.Errorf("metadata partition present but metadata file ICB not set")
	}

	entry, entryData, err := r.readFileEntryWithData(*r.metadataFileICB)
	if err != nil {
		return nil, fmt.Errorf("failed to read metadata file entry: %w", err)
	}
	allocs := r.readAllocationDescriptors(entry, entryData, 0)
	if len(allocs) == 0 {
		return nil, fmt.Errorf("metadata file has no allocation descriptors")
	}
	r.metadataFileAllocDescs = allocs
	return allocs, nil
}

// Volume Recognition Descriptor
type VolumeRecognitionDescriptor struct {
	StructureType      uint8
	StandardIdentifier [5]byte
	StructureVersion   uint8
	Reserved           byte
	StructureData      [2040]byte
}

// Anchor Volume Descriptor Pointer
type AnchorVolumeDescriptorPointer struct {
	DescriptorTag                         Tag
	MainVolumeDescriptorSequenceExtent    ExtentAD
	ReserveVolumeDescriptorSequenceExtent ExtentAD
	Reserved                              [480]byte
}

// Primary Volume Descriptor
type PrimaryVolumeDescriptor struct {
	DescriptorTag                               Tag
	VolumeDescriptorSequenceNumber              uint32
	PrimaryVolumeDescriptorNumber               uint32
	VolumeIdentifier                            [32]byte
	VolumeSequenceNumber                        uint16
	MaximumVolumeSequenceNumber                 uint16
	InterchangeLevel                            uint16
	MaximumInterchangeLevel                     uint16
	CharacterSetList                            uint32
	MaximumCharacterSetList                     uint32
	VolumeSetIdentifier                         [128]byte
	DescriptorCharacterSet                      CharSpec
	ExplanatoryCharacterSet                     CharSpec
	VolumeAbstract                              ExtentAD
	VolumeCopyrightNotice                       ExtentAD
	ApplicationIdentifier                       EntityID
	RecordingDateAndTime                        Timestamp
	ImplementationIdentifier                    EntityID
	ImplementationUse                           [64]byte
	PredecessorVolumeDescriptorSequenceLocation uint32
	Flags                                       uint16
	Reserved                                    [22]byte
}

// Partition Descriptor
type PartitionDescriptor struct {
	DescriptorTag                  Tag
	VolumeDescriptorSequenceNumber uint32
	PartitionFlags                 uint16
	PartitionNumber                uint16
	PartitionContents              EntityID
	PartitionContentsUse           [128]byte
	AccessType                     uint32
	PartitionStartingLocation      uint32
	PartitionLength                uint32
	ImplementationIdentifier       EntityID
	ImplementationUse              [128]byte
	Reserved                       [156]byte
}

// Logical Volume Descriptor
type LogicalVolumeDescriptor struct {
	DescriptorTag                  Tag
	VolumeDescriptorSequenceNumber uint32
	DescriptorCharacterSet         CharSpec
	LogicalVolumeIdentifier        [128]byte
	LogicalBlockSize               uint32
	DomainIdentifier               EntityID
	LogicalVolumeContentsUse       [16]byte
	MapTableLength                 uint32
	NumberOfPartitionMaps          uint32
	ImplementationIdentifier       EntityID
	ImplementationUse              [128]byte
	IntegritySequenceExtent        ExtentAD
	// Partition maps follow (variable length)
}

// File Set Descriptor
type FileSetDescriptor struct {
	DescriptorTag                       Tag
	RecordingDateAndTime                Timestamp
	InterchangeLevel                    uint16
	MaximumInterchangeLevel             uint16
	CharacterSetList                    uint32
	MaximumCharacterSetList             uint32
	FileSetNumber                       uint32
	FileSetDescriptorNumber             uint32
	LogicalVolumeIdentifierCharacterSet CharSpec
	LogicalVolumeIdentifier             [128]byte
	FileSetCharacterSet                 CharSpec
	FileSetIdentifier                   [32]byte
	CopyrightFileIdentifier             [32]byte
	AbstractFileIdentifier              [32]byte
	RootDirectoryICB                    LongAD
	DomainIdentifier                    EntityID
	NextExtent                          LongAD
	SystemStreamDirectoryICB            LongAD
	Reserved                            [32]byte
}
