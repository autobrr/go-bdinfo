package udf

// UDF constants for Blu-ray disc support (UDF 2.50+)
const (
	// Sector size for optical media
	SectorSize = 2048

	// Volume Recognition Sequence
	VRSOffset = 16 * SectorSize // Starting at sector 16

	// Standard identifiers
	StandardIDBEA01 = "BEA01"
	StandardIDNSR02 = "NSR02"
	StandardIDNSR03 = "NSR03"
	StandardIDTEA01 = "TEA01"

	// Descriptor tags
	TagPrimaryVolume        = 1
	TagAnchorVolume         = 2
	TagVolumePointer        = 3
	TagImplementationVolume = 4
	TagPartition            = 5
	TagLogicalVolume        = 6
	TagUnallocatedSpace     = 7
	TagTerminating          = 8
	TagFileSet              = 256
	TagFileIdentifier       = 257
	TagAllocationExtent     = 258
	TagIndirect             = 259
	TagTerminalEntry        = 260
	TagFile                 = 261
	TagExtendedAttribute    = 262
	TagExtendedFileEntry    = 266

	// File characteristics
	FileCharHidden    = 0x01
	FileCharDirectory = 0x02
	FileCharDeleted   = 0x04
	FileCharParent    = 0x08
	FileCharMetadata  = 0x10

	// ICB flags
	ICBFileTypeDirectory = 4
	ICBFileTypeFile      = 5
)

// EntityID represents UDF entity identifier
type EntityID struct {
	Flags      byte
	Identifier [23]byte
	Suffix     [8]byte
}

// ExtentAD represents extent address descriptor
type ExtentAD struct {
	Length   uint32
	Location uint32
}

// LBAddr represents a logical block address (ECMA-167).
// LogicalBlockNumber is relative to the referenced partition.
type LBAddr struct {
	LogicalBlockNumber       uint32
	PartitionReferenceNumber uint16
}

// LongAD represents long allocation descriptor
type LongAD struct {
	ExtentLength      uint32
	ExtentLocation    LBAddr
	ImplementationUse [6]byte
}

// ShortAD represents short allocation descriptor
type ShortAD struct {
	ExtentLength   uint32
	ExtentPosition uint32
}

// Timestamp represents UDF timestamp (12 bytes)
type Timestamp struct {
	TypeAndTimezone        uint16 // Bits 12-15: Type, Bits 0-11: Timezone
	Year                   uint16
	Month                  uint8
	Day                    uint8
	Hour                   uint8
	Minute                 uint8
	Second                 uint8
	Centiseconds           uint8
	HundredsOfMicroseconds uint8
	Microseconds           uint8
}

// Tag represents descriptor tag
type Tag struct {
	TagIdentifier       uint16
	DescriptorVersion   uint16
	TagChecksum         uint8
	Reserved            uint8
	TagSerialNumber     uint16
	DescriptorCRC       uint16
	DescriptorCRCLength uint16
	TagLocation         uint32
}

// CharSpec represents character set specification
type CharSpec struct {
	CharacterSetType uint8
	CharacterSetInfo [63]byte
}
