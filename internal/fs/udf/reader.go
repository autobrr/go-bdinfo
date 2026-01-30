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
	rootICB         LongAD
	fileSetDesc     *FileSetDescriptor
	fileSetLocation uint32
}

// NewReader creates a new UDF reader
func NewReader(path string) (*Reader, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("failed to open ISO file: %w", err)
	}

	reader := &Reader{
		file:      file,
		blockSize: SectorSize,
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
			r.partitionStart = pd.PartitionStartingLocation
			r.partitionSize = pd.PartitionLength

		case TagLogicalVolume:
			var lvd LogicalVolumeDescriptor
			if err := r.readDescriptor(&lvd); err != nil {
				return err
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
func (r *Reader) readDescriptor(desc interface{}) error {
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

	// Simple implementation - assumes ASCII for now
	// Full implementation would handle compression types
	compType := data[0]
	if compType == 8 || compType == 16 {
		// Latin-1 or UCS-2
		result := strings.TrimRight(string(data[1:]), "\x00 ")
		return result
	}

	return ""
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
	DescriptorTag                      Tag
	MainVolumeDescriptorSequenceExtent ExtentAD
	ReserveVolumeDescriptorSequenceExtent ExtentAD
	Reserved                           [480]byte
}

// Primary Volume Descriptor
type PrimaryVolumeDescriptor struct {
	DescriptorTag                   Tag
	VolumeDescriptorSequenceNumber  uint32
	PrimaryVolumeDescriptorNumber   uint32
	VolumeIdentifier                [32]byte
	VolumeSequenceNumber            uint16
	MaximumVolumeSequenceNumber     uint16
	InterchangeLevel                uint16
	MaximumInterchangeLevel         uint16
	CharacterSetList                uint32
	MaximumCharacterSetList         uint32
	VolumeSetIdentifier             [128]byte
	DescriptorCharacterSet          CharSpec
	ExplanatoryCharacterSet         CharSpec
	VolumeAbstract                  ExtentAD
	VolumeCopyrightNotice           ExtentAD
	ApplicationIdentifier           EntityID
	RecordingDateAndTime            Timestamp
	ImplementationIdentifier        EntityID
	ImplementationUse               [64]byte
	PredecessorVolumeDescriptorSequenceLocation uint32
	Flags                           uint16
	Reserved                        [22]byte
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
	DescriptorTag                 Tag
	RecordingDateAndTime          Timestamp
	InterchangeLevel              uint16
	MaximumInterchangeLevel       uint16
	CharacterSetList              uint32
	MaximumCharacterSetList       uint32
	FileSetNumber                 uint32
	FileSetDescriptorNumber       uint32
	LogicalVolumeIdentifierCharacterSet CharSpec
	LogicalVolumeIdentifier       [128]byte
	FileSetCharacterSet           CharSpec
	FileSetIdentifier             [32]byte
	CopyrightFileIdentifier       [32]byte
	AbstractFileIdentifier        [32]byte
	RootDirectoryICB              LongAD
	DomainIdentifier              EntityID
	NextExtent                    LongAD
	SystemStreamDirectoryICB      LongAD
	Reserved                      [32]byte
}