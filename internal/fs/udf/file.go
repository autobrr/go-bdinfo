package udf

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"io"
	"path"
	"strings"
	"sync"
	"time"
)

func (r *Reader) readFullAt(off int64, p []byte) error {
	sr := io.NewSectionReader(r.file, off, int64(len(p)))
	_, err := io.ReadFull(sr, p)
	return err
}

func (r *Reader) readBlock(block uint32) ([]byte, error) {
	if r.blockSize == 0 {
		return nil, fmt.Errorf("udf: block size not set")
	}
	b := make([]byte, r.blockSize)
	if err := r.readFullAt(int64(block)*int64(r.blockSize), b); err != nil {
		return nil, err
	}
	return b, nil
}

// File represents a file in the UDF file system
type File struct {
	reader       *Reader
	Name         string // Exported for external access
	icb          LongAD
	size         int64
	modTime      time.Time
	isDirectory  bool
	sizeKnown    bool
	modTimeKnown bool
}

// Directory represents a directory in the UDF file system
type Directory struct {
	reader  *Reader
	Name    string // Exported for external access
	path    string
	icb     LongAD
	entries []*FileIdentifierDescriptor

	entriesOnce sync.Once
	entriesErr  error
}

func (d *Directory) ensureEntries() error {
	d.entriesOnce.Do(func() {
		d.entriesErr = d.readEntries()
	})
	return d.entriesErr
}

// Size returns the file size in bytes.
func (f *File) Size() int64 {
	if f.sizeKnown {
		return f.size
	}
	entry, err := f.reader.readFileEntry(f.icb)
	if err != nil {
		return 0
	}
	switch e := entry.(type) {
	case *FileEntry:
		f.size = int64(e.InformationLength)
		f.modTime = convertTimestamp(e.ModificationTime)
	case *ExtendedFileEntry:
		f.size = int64(e.InformationLength)
		f.modTime = convertTimestamp(e.ModificationTime)
	}
	f.sizeKnown = true
	f.modTimeKnown = true
	return f.size
}

// ModTime returns the modification time.
func (f *File) ModTime() time.Time {
	if f.modTimeKnown {
		return f.modTime
	}
	_ = f.Size()
	if f.modTimeKnown {
		return f.modTime
	}
	return time.Time{}
}

// FileEntry represents a UDF file entry
type FileEntry struct {
	DescriptorTag                 Tag
	ICBTag                        ICBTag
	UID                           uint32
	GID                           uint32
	Permissions                   uint32
	FileLinkCount                 uint16
	RecordFormat                  uint8
	RecordDisplayAttributes       uint8
	RecordLength                  uint32
	InformationLength             uint64
	LogicalBlocksRecorded         uint64
	AccessTime                    Timestamp
	ModificationTime              Timestamp
	AttributeTime                 Timestamp
	Checkpoint                    uint32
	ExtendedAttributeICB          LongAD
	ImplementationIdentifier      EntityID
	UniqueID                      uint64
	LengthOfExtendedAttributes    uint32
	LengthOfAllocationDescriptors uint32
	// Extended attributes and allocation descriptors follow
}

// ExtendedFileEntry for large files
type ExtendedFileEntry struct {
	DescriptorTag                 Tag
	ICBTag                        ICBTag
	UID                           uint32
	GID                           uint32
	Permissions                   uint32
	FileLinkCount                 uint16
	RecordFormat                  uint8
	RecordDisplayAttributes       uint8
	RecordLength                  uint32
	InformationLength             uint64
	ObjectSize                    uint64
	LogicalBlocksRecorded         uint64
	AccessTime                    Timestamp
	ModificationTime              Timestamp
	CreateTime                    Timestamp
	AttributeTime                 Timestamp
	Checkpoint                    uint32
	Reserved                      [4]byte
	ExtendedAttributeICB          LongAD
	StreamDirectoryICB            LongAD
	ImplementationIdentifier      EntityID
	UniqueID                      uint64
	LengthOfExtendedAttributes    uint32
	LengthOfAllocationDescriptors uint32
}

// ICBTag represents Information Control Block tag
type ICBTag struct {
	PriorRecordedNumberOfDirectEntries uint32  // 0-3
	StrategyType                       uint16  // 4-5
	StrategyParameter                  [2]byte // 6-7
	MaximumNumberOfEntries             uint16  // 8-9
	Reserved                           byte    // 10
	FileType                           uint8   // 11
	ParentICBLocation                  LBAddr  // 12-17 (6 bytes)
	Flags                              uint16  // 18-19
}

// FileIdentifierDescriptor represents a file identifier
type FileIdentifierDescriptor struct {
	DescriptorTag             Tag
	FileVersionNumber         uint16
	FileCharacteristics       uint8
	LengthOfFileIdentifier    uint8
	ICB                       LongAD
	LengthOfImplementationUse uint16
	// Implementation use and file identifier follow
	fileName string // Parsed file name
}

// ReadDirectory reads a directory's contents
func (r *Reader) ReadDirectory(dirPath string) (*Directory, error) {
	// For root directory
	if dirPath == "/" || dirPath == "" {
		if r.fileSetDesc == nil {
			// Need to read file set descriptor first
			// This would typically be done during initialization
			return nil, fmt.Errorf("file set descriptor not loaded")
		}

		dir := &Directory{
			reader: r,
			Name:   "",
			path:   "/",
			icb:    r.rootICB,
		}

		if err := dir.ensureEntries(); err != nil {
			return nil, err
		}

		return dir, nil
	}

	// Traverse from root for subdirectories
	dirPath = strings.TrimPrefix(dirPath, "/")
	parts := strings.Split(dirPath, "/")
	currentDir, err := r.ReadDirectory("/")
	if err != nil {
		return nil, err
	}

	for _, part := range parts {
		if part == "" {
			continue
		}
		dirs, err := currentDir.GetDirectories()
		if err != nil {
			return nil, err
		}

		found := false
		for _, dir := range dirs {
			if strings.EqualFold(dir.Name, part) {
				if err := dir.ensureEntries(); err != nil {
					return nil, err
				}
				currentDir = dir
				found = true
				break
			}
		}
		if !found {
			return nil, fmt.Errorf("directory not found: %s", part)
		}
	}

	return currentDir, nil
}

// readEntries reads all entries in a directory
func (d *Directory) readEntries() error {
	// Read the directory's file entry
	fileEntry, fileEntryData, err := d.reader.readFileEntryWithData(d.icb)
	if err != nil {
		return err
	}

	// Check if data is embedded in ICB
	var icbFlags uint16
	var allocDescLength uint32
	var extAttribLength uint32

	switch e := fileEntry.(type) {
	case *FileEntry:
		icbFlags = e.ICBTag.Flags
		allocDescLength = e.LengthOfAllocationDescriptors
		extAttribLength = e.LengthOfExtendedAttributes
	case *ExtendedFileEntry:
		icbFlags = e.ICBTag.Flags
		allocDescLength = e.LengthOfAllocationDescriptors
		extAttribLength = e.LengthOfExtendedAttributes
	}

	allocType := (icbFlags >> 0) & 0x7

	// Check if information length indicates there's data but no allocation descriptors
	var infoLength uint64
	switch e := fileEntry.(type) {
	case *FileEntry:
		infoLength = e.InformationLength
	case *ExtendedFileEntry:
		infoLength = e.InformationLength
	}

	if allocType == 3 || (allocDescLength == 0 && infoLength > 0) {
		// Data is embedded - read it directly from the file entry

		// Calculate offset to embedded data
		var baseSize int64
		switch fileEntry.(type) {
		case *FileEntry:
			baseSize = 176
		case *ExtendedFileEntry:
			baseSize = 216
		}

		embeddedOffset := baseSize + int64(extAttribLength)
		embeddedEnd := embeddedOffset + int64(allocDescLength)
		if embeddedOffset < 0 || embeddedEnd > int64(len(fileEntryData)) {
			return fmt.Errorf("embedded directory data out of range (offset=%d length=%d)", embeddedOffset, allocDescLength)
		}
		embeddedData := fileEntryData[embeddedOffset:embeddedEnd]

		// Use a custom method for embedded data
		if err := d.readEmbeddedDirectoryData(embeddedData); err != nil {
			return err
		}
	} else {
		// Read allocation descriptors to get data location
		allocDescs := d.reader.readAllocationDescriptors(fileEntry, fileEntryData, d.icb.ExtentLocation.PartitionReferenceNumber)

		// Read directory entries
		for _, ad := range allocDescs {
			if err := d.readDirectoryData(ad); err != nil {
				return err
			}
		}
	}

	return nil
}

type allocationDescriptor struct {
	// length in bytes (top 2 bits cleared)
	length uint32
	lbn    uint32
	pref   uint16
}

func (r *Reader) readFileEntryWithData(icb LongAD) (any, []byte, error) {
	location, err := r.resolveLBAddr(icb.ExtentLocation)
	if err != nil {
		return nil, nil, err
	}

	block, err := r.readBlock(location)
	if err != nil {
		return nil, nil, err
	}

	var tag Tag
	if err := binary.Read(bytes.NewReader(block[:binary.Size(Tag{})]), binary.LittleEndian, &tag); err != nil {
		return nil, nil, err
	}

	switch tag.TagIdentifier {
	case TagFile:
		var fe FileEntry
		if err := binary.Read(bytes.NewReader(block), binary.LittleEndian, &fe); err != nil {
			return nil, nil, err
		}
		return &fe, block, nil

	case TagExtendedFileEntry:
		var efe ExtendedFileEntry
		if err := binary.Read(bytes.NewReader(block), binary.LittleEndian, &efe); err != nil {
			return nil, nil, err
		}
		return &efe, block, nil

	default:
		return nil, nil, fmt.Errorf("unexpected tag type: %d at location %d", tag.TagIdentifier, location)
	}
}

// readFileEntry reads a file entry from an ICB.
func (r *Reader) readFileEntry(icb LongAD) (any, error) {
	entry, _, err := r.readFileEntryWithData(icb)
	return entry, err
}

// readAllocationDescriptors extracts allocation descriptors from a file entry.
// If the descriptor format doesn't contain a partition reference (short_ad), defaultPref is used.
func (r *Reader) readAllocationDescriptors(entry any, entryData []byte, defaultPref uint16) []allocationDescriptor {

	var allocDescLength uint32
	var extAttribLength uint32
	var icbFlags uint16

	switch e := entry.(type) {
	case *FileEntry:
		allocDescLength = e.LengthOfAllocationDescriptors
		extAttribLength = e.LengthOfExtendedAttributes
		icbFlags = e.ICBTag.Flags

	case *ExtendedFileEntry:
		allocDescLength = e.LengthOfAllocationDescriptors
		extAttribLength = e.LengthOfExtendedAttributes
		icbFlags = e.ICBTag.Flags
	default:
		return nil
	}

	if allocDescLength == 0 {
		return nil
	}

	// Determine allocation descriptor type from ICB flags
	allocType := (icbFlags >> 0) & 0x7

	// Type 3 means data is embedded in the ICB itself
	if allocType == 3 {
		// For embedded data, the directory entries are stored directly
		// after the file entry, not in separate extents
		return nil // Will handle this differently
	}

	// Handle different allocation descriptor types
	switch allocType {
	case 0: // Short allocation descriptors (8 bytes each)
		// Continue with existing code
	case 1: // Long allocation descriptors (16 bytes each)
		// Continue with long AD parsing
	case 2: // Extended allocation descriptors (20 bytes each)
		// TODO: Implement extended descriptors
		return nil
	default:
		return nil
	}

	// Calculate offset to allocation descriptors
	// They come after the fixed part of the file entry and extended attributes
	var baseSize int64
	switch entry.(type) {
	case *FileEntry:
		baseSize = 176 // Size of FileEntry up to but not including extended attributes
	case *ExtendedFileEntry:
		baseSize = 216 // Size of ExtendedFileEntry up to but not including extended attributes
	}

	allocDescOffset := baseSize + int64(extAttribLength)
	allocDescEnd := allocDescOffset + int64(allocDescLength)
	if allocDescOffset < 0 || allocDescEnd > int64(len(entryData)) {
		return nil
	}
	allocData := entryData[allocDescOffset:allocDescEnd]

	switch allocType {
	case 0:
		numDescs := allocDescLength / 8
		descs := make([]allocationDescriptor, 0, numDescs)
		rd := bytes.NewReader(allocData)
		for range numDescs {
			var sad ShortAD
			if err := binary.Read(rd, binary.LittleEndian, &sad); err != nil {
				break
			}
			descs = append(descs, allocationDescriptor{
				length: sad.ExtentLength & 0x3FFFFFFF,
				lbn:    sad.ExtentPosition,
				pref:   defaultPref,
			})
		}
		return descs

	case 1:
		numDescs := allocDescLength / 16
		descs := make([]allocationDescriptor, 0, numDescs)
		rd := bytes.NewReader(allocData)
		for range numDescs {
			var lad LongAD
			if err := binary.Read(rd, binary.LittleEndian, &lad); err != nil {
				break
			}
			descs = append(descs, allocationDescriptor{
				length: lad.ExtentLength & 0x3FFFFFFF,
				lbn:    lad.ExtentLocation.LogicalBlockNumber,
				pref:   lad.ExtentLocation.PartitionReferenceNumber,
			})
		}
		return descs
	}

	return nil
}

// readDirectoryData reads directory entries from an allocation descriptor
func (d *Directory) readDirectoryData(ad allocationDescriptor) error {
	location, err := d.reader.resolvePartitionBlock(ad.pref, ad.lbn)
	if err != nil {
		return err
	}
	length := ad.length

	// Read all directory data
	data := make([]byte, length)
	if err := d.reader.readFullAt(int64(location)*int64(d.reader.blockSize), data); err != nil {
		return err
	}

	// Parse FileIdentifierDescriptors
	offset := uint32(0)
	for offset < length {
		if offset+38 > length { // Minimum FID size
			break
		}

		// Read FID header
		fid := &FileIdentifierDescriptor{}
		fidReader := bytes.NewReader(data[offset:])

		// Read fixed part of FID
		binary.Read(fidReader, binary.LittleEndian, &fid.DescriptorTag)
		binary.Read(fidReader, binary.LittleEndian, &fid.FileVersionNumber)
		binary.Read(fidReader, binary.LittleEndian, &fid.FileCharacteristics)
		binary.Read(fidReader, binary.LittleEndian, &fid.LengthOfFileIdentifier)
		binary.Read(fidReader, binary.LittleEndian, &fid.ICB)
		binary.Read(fidReader, binary.LittleEndian, &fid.LengthOfImplementationUse)

		// Skip implementation use
		fidReader.Seek(int64(fid.LengthOfImplementationUse), io.SeekCurrent)

		// Read file identifier (name)
		if fid.LengthOfFileIdentifier > 0 {
			nameData := make([]byte, fid.LengthOfFileIdentifier)
			fidReader.Read(nameData)

			// Parse the name (simplified - assumes ASCII)
			// First byte is compression type (usually 8 for 8-bit)
			if len(nameData) > 1 && nameData[0] == 8 {
				name := string(nameData[1:])
				// Remove null terminator if present
				if idx := strings.IndexByte(name, 0); idx >= 0 {
					name = name[:idx]
				}
				fid.fileName = name
			}
		}

		// Store the FID
		d.entries = append(d.entries, fid)

		// Calculate total FID size (must be 4-byte aligned)
		fidSize := uint32(38) + uint32(fid.LengthOfImplementationUse) + uint32(fid.LengthOfFileIdentifier)
		fidSize = (fidSize + 3) &^ 3 // Round up to 4-byte boundary

		offset += fidSize
	}

	return nil
}

// GetFiles returns all files in the directory
func (d *Directory) GetFiles() ([]*File, error) {
	if err := d.ensureEntries(); err != nil {
		return nil, err
	}

	var files []*File

	for _, entry := range d.entries {
		if entry.FileCharacteristics&FileCharDirectory == 0 {
			// It's a file
			name := d.getFileName(entry)
			file := &File{
				reader:      d.reader,
				Name:        name,
				icb:         entry.ICB,
				isDirectory: false,
			}
			files = append(files, file)
		}
	}

	return files, nil
}

// GetDirectories returns all subdirectories
func (d *Directory) GetDirectories() ([]*Directory, error) {
	if err := d.ensureEntries(); err != nil {
		return nil, err
	}

	var dirs []*Directory

	for _, entry := range d.entries {
		if entry.FileCharacteristics&FileCharDirectory != 0 &&
			entry.FileCharacteristics&FileCharParent == 0 {
			// It's a directory (not parent)
			name := d.getFileName(entry)
			subdir := &Directory{
				reader: d.reader,
				Name:   name,
				path:   path.Join(d.path, name),
				icb:    entry.ICB,
			}
			dirs = append(dirs, subdir)
		}
	}

	return dirs, nil
}

// getFileName extracts the file name from a FileIdentifierDescriptor
func (d *Directory) getFileName(fid *FileIdentifierDescriptor) string {
	if fid.fileName != "" {
		return fid.fileName
	}
	return "unknown"
}

// Open opens the file for reading
func (f *File) Open() (io.ReadCloser, error) {
	// Read the file entry
	entry, entryData, err := f.reader.readFileEntryWithData(f.icb)
	if err != nil {
		return nil, err
	}

	var infoLen uint64
	switch e := entry.(type) {
	case *FileEntry:
		infoLen = e.InformationLength
	case *ExtendedFileEntry:
		infoLen = e.InformationLength
	}

	// Get allocation descriptors
	allocDescs := f.reader.readAllocationDescriptors(entry, entryData, f.icb.ExtentLocation.PartitionReferenceNumber)
	if len(allocDescs) == 0 {
		return nil, fmt.Errorf("no allocation descriptors found")
	}

	size := int64(infoLen)
	if size < 0 {
		size = 0
	}

	exts := make([]extent, 0, len(allocDescs))
	var fileOff int64
	for _, ad := range allocDescs {
		if ad.length == 0 {
			continue
		}
		if fileOff >= size {
			break
		}
		loc, err := f.reader.resolvePartitionBlock(ad.pref, ad.lbn)
		if err != nil {
			return nil, err
		}
		segLen := int64(ad.length)
		if segLen < 0 {
			continue
		}
		if fileOff+segLen > size {
			segLen = size - fileOff
		}
		exts = append(exts, extent{
			fileStart: fileOff,
			fileEnd:   fileOff + segLen,
			physOff:   int64(loc) * int64(f.reader.blockSize),
		})
		fileOff += segLen
	}
	if len(exts) == 0 {
		return &fileReader{reader: f.reader, offset: 0, size: 0}, nil
	}
	if len(exts) == 1 && exts[0].fileStart == 0 {
		return &fileReader{reader: f.reader, offset: exts[0].physOff, size: exts[0].fileEnd}, nil
	}
	return &extentReader{
		reader:  f.reader,
		extents: exts,
		size:    size,
	}, nil
}

// fileReader implements io.ReadCloser for UDF files
type fileReader struct {
	reader   *Reader
	offset   int64
	size     int64
	position int64
}

func (fr *fileReader) Read(p []byte) (n int, err error) {
	if fr.position >= fr.size {
		return 0, io.EOF
	}

	// Read up to remaining size
	toRead := len(p)
	if remaining := fr.size - fr.position; int64(toRead) > remaining {
		toRead = int(remaining)
	}

	n, err = fr.reader.file.ReadAt(p[:toRead], fr.offset+fr.position)
	fr.position += int64(n)

	if fr.position >= fr.size && err == nil {
		err = io.EOF
	}

	return n, err
}

func (fr *fileReader) Close() error {
	// Nothing to close - the main file handle stays open
	return nil
}

type extent struct {
	fileStart int64
	fileEnd   int64
	physOff   int64
}

type extentReader struct {
	reader  *Reader
	extents []extent
	size    int64

	pos int64
	idx int
}

func (er *extentReader) Read(p []byte) (n int, err error) {
	if er.pos >= er.size {
		return 0, io.EOF
	}

	toRead := len(p)
	if remaining := er.size - er.pos; int64(toRead) > remaining {
		toRead = int(remaining)
	}

	for n < toRead {
		if er.idx >= len(er.extents) {
			if n == 0 {
				return 0, io.EOF
			}
			return n, io.EOF
		}
		ex := er.extents[er.idx]
		if er.pos >= ex.fileEnd {
			er.idx++
			continue
		}
		if er.pos < ex.fileStart {
			// Shouldn't happen for sequential extents, but avoid loops if it does.
			er.pos = ex.fileStart
		}

		inExtent := ex.fileEnd - er.pos
		want := toRead - n
		if int64(want) > inExtent {
			want = int(inExtent)
		}

		off := ex.physOff + (er.pos - ex.fileStart)
		nn, rerr := er.reader.file.ReadAt(p[n:n+want], off)
		n += nn
		er.pos += int64(nn)
		if rerr != nil {
			if rerr == io.EOF {
				er.idx++
				continue
			}
			return n, rerr
		}
		if nn < want {
			er.idx++
		}
	}

	if er.pos >= er.size {
		return n, io.EOF
	}
	return n, nil
}

func (er *extentReader) Close() error { return nil }

// convertTimestamp converts UDF timestamp to Go time.Time
func convertTimestamp(ts Timestamp) time.Time {
	// Simplified conversion
	return time.Date(
		int(ts.Year),
		time.Month(ts.Month),
		int(ts.Day),
		int(ts.Hour),
		int(ts.Minute),
		int(ts.Second),
		int(ts.Microseconds)*1000,
		time.UTC,
	)
}

// FindFile searches for a file by path
func (r *Reader) FindFile(filePath string) (*File, error) {
	// Normalize path
	filePath = strings.TrimPrefix(filePath, "/")
	parts := strings.Split(filePath, "/")

	// Start from root
	currentDir, err := r.ReadDirectory("/")
	if err != nil {
		return nil, err
	}

	// Traverse path
	for i, part := range parts {
		if i == len(parts)-1 {
			// Last part - should be a file
			files, err := currentDir.GetFiles()
			if err != nil {
				return nil, err
			}

			for _, file := range files {
				if strings.EqualFold(file.Name, part) {
					return file, nil
				}
			}
			return nil, fmt.Errorf("file not found: %s", part)
		} else {
			// Directory part
			dirs, err := currentDir.GetDirectories()
			if err != nil {
				return nil, err
			}

			found := false
			for _, dir := range dirs {
				if strings.EqualFold(dir.Name, part) {
					currentDir = dir
					if err := currentDir.ensureEntries(); err != nil {
						return nil, err
					}
					found = true
					break
				}
			}

			if !found {
				return nil, fmt.Errorf("directory not found: %s", part)
			}
		}
	}

	return nil, fmt.Errorf("file not found")
}

// readEmbeddedDirectoryData reads directory entries from embedded data
func (d *Directory) readEmbeddedDirectoryData(data []byte) error {
	// Parse FileIdentifierDescriptors
	offset := uint32(0)
	length := uint32(len(data))

	for offset < length {
		if offset+38 > length { // Minimum FID size
			break
		}

		// Read FID header with correct field offsets
		fid := &FileIdentifierDescriptor{}

		// Manual parsing to ensure correct offsets
		if offset+38 > uint32(len(data)) {
			break
		}

		// Tag (16 bytes, offset 0-15)
		fid.DescriptorTag.TagIdentifier = binary.LittleEndian.Uint16(data[offset : offset+2])
		fid.DescriptorTag.DescriptorVersion = binary.LittleEndian.Uint16(data[offset+2 : offset+4])
		fid.DescriptorTag.TagChecksum = data[offset+4]
		fid.DescriptorTag.Reserved = data[offset+5]
		fid.DescriptorTag.TagSerialNumber = binary.LittleEndian.Uint16(data[offset+6 : offset+8])
		fid.DescriptorTag.DescriptorCRC = binary.LittleEndian.Uint16(data[offset+8 : offset+10])
		fid.DescriptorTag.DescriptorCRCLength = binary.LittleEndian.Uint16(data[offset+10 : offset+12])
		fid.DescriptorTag.TagLocation = binary.LittleEndian.Uint32(data[offset+12 : offset+16])

		// FileVersionNumber (2 bytes, offset 16-17)
		fid.FileVersionNumber = binary.LittleEndian.Uint16(data[offset+16 : offset+18])

		// FileCharacteristics (1 byte, offset 18)
		fid.FileCharacteristics = data[offset+18]

		// LengthOfFileIdentifier (1 byte, offset 19)
		fid.LengthOfFileIdentifier = data[offset+19]

		// ICB LongAD (16 bytes, offset 20-35)
		fid.ICB.ExtentLength = binary.LittleEndian.Uint32(data[offset+20 : offset+24])
		fid.ICB.ExtentLocation.LogicalBlockNumber = binary.LittleEndian.Uint32(data[offset+24 : offset+28])
		fid.ICB.ExtentLocation.PartitionReferenceNumber = binary.LittleEndian.Uint16(data[offset+28 : offset+30])
		copy(fid.ICB.ImplementationUse[:], data[offset+30:offset+36])

		// LengthOfImplementationUse (2 bytes, offset 36-37)
		fid.LengthOfImplementationUse = binary.LittleEndian.Uint16(data[offset+36 : offset+38])

		// Read file identifier (name) - skip implementation use data first
		nameOffset := 38 + int(fid.LengthOfImplementationUse)
		if fid.LengthOfFileIdentifier > 0 && offset+uint32(nameOffset)+uint32(fid.LengthOfFileIdentifier) <= uint32(len(data)) {
			nameData := data[offset+uint32(nameOffset) : offset+uint32(nameOffset)+uint32(fid.LengthOfFileIdentifier)]

			// Parse the name (simplified - assumes ASCII)
			// First byte is compression type (usually 8 for 8-bit)
			if len(nameData) > 1 && nameData[0] == 8 {
				name := string(nameData[1:])
				// Remove null terminator if present
				if idx := strings.IndexByte(name, 0); idx >= 0 {
					name = name[:idx]
				}
				fid.fileName = name
			}
		}

		// Store the FID
		d.entries = append(d.entries, fid)

		// Calculate total FID size (must be 4-byte aligned)
		fidSize := uint32(38) + uint32(fid.LengthOfImplementationUse) + uint32(fid.LengthOfFileIdentifier)
		fidSize = (fidSize + 3) &^ 3 // Round up to 4-byte boundary

		offset += fidSize
	}

	return nil
}

// tryReadBlurayRootDirectory attempts to read root directory using Blu-ray specific layout
func (d *Directory) tryReadBlurayRootDirectory() error {
	// Blu-ray discs often store directory data immediately after the FileSet descriptor
	// FileSet is typically at partition sector 32 for BD-ROM, but prefer the actual parsed location.
	fileSetLocation := d.reader.partitionStart + d.reader.fileSetLocation
	if d.reader.fileSetLocation == 0 {
		fileSetLocation = d.reader.partitionStart + 32
	}

	// Try several potential locations for directory data
	locations := []int64{
		int64(fileSetLocation)*int64(d.reader.blockSize) + 256,  // FileSet + 256 bytes
		int64(fileSetLocation)*int64(d.reader.blockSize) + 512,  // FileSet + 512 bytes
		int64(fileSetLocation)*int64(d.reader.blockSize) + 1024, // FileSet + 1024 bytes
		int64(fileSetLocation+1) * int64(d.reader.blockSize),    // Next sector after FileSet
	}

	for _, loc := range locations {
		if _, err := d.reader.file.Seek(loc, io.SeekStart); err != nil {
			continue
		}

		// Read a small amount to check for FID tag
		header := make([]byte, 4)
		if _, err := d.reader.file.Read(header); err != nil {
			continue
		}

		tag := binary.LittleEndian.Uint16(header[0:2])
		if tag == TagFileIdentifier {
			// Found FID! Read more data and parse
			d.reader.file.Seek(loc, io.SeekStart)
			data := make([]byte, 2048) // Read one sector
			if _, err := d.reader.file.Read(data); err != nil {
				continue
			}

			// Parse the directory data
			return d.readEmbeddedDirectoryData(data)
		}
	}

	return fmt.Errorf("no directory data found at expected Blu-ray locations")
}
