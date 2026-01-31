package fs

import (
	"fmt"
	"io"
	"path"
	"path/filepath"
	"strings"
	"time"

	"github.com/s0up4200/go-bdinfo/internal/fs/udf"
)

// ISOFileSystemImpl implements ISOFileSystem for reading ISO files.
type ISOFileSystemImpl struct {
	isoPath     string
	volumeLabel string
	mounted     bool
	udfReader   *udf.Reader
	// Cache for directory lookups
	dirCache map[string]*udf.Directory
}

// NewISOFileSystem creates a new ISO file system reader.
func NewISOFileSystem() ISOFileSystem {
	return &ISOFileSystemImpl{
		dirCache: make(map[string]*udf.Directory),
	}
}

// Mount opens the ISO file and prepares it for reading.
func (fs *ISOFileSystemImpl) Mount(isoPath string) error {
	if fs.mounted {
		return fmt.Errorf("ISO already mounted")
	}

	// Open UDF reader
	reader, err := udf.NewReader(isoPath)
	if err != nil {
		return fmt.Errorf("failed to open UDF volume: %w", err)
	}

	fs.udfReader = reader
	fs.isoPath = isoPath
	fs.volumeLabel = reader.GetVolumeLabel()
	fs.mounted = true

	return nil
}

// Unmount closes the ISO file.
func (fs *ISOFileSystemImpl) Unmount() error {
	if !fs.mounted {
		return nil
	}

	if fs.udfReader != nil {
		if err := fs.udfReader.Close(); err != nil {
			return err
		}
		fs.udfReader = nil
	}
	fs.mounted = false
	fs.dirCache = make(map[string]*udf.Directory)
	return nil
}

// GetVolumeLabel returns the volume label of the ISO.
func (fs *ISOFileSystemImpl) GetVolumeLabel() string {
	return fs.volumeLabel
}

// GetDirectoryInfo returns information about a directory in the ISO.
func (fs *ISOFileSystemImpl) GetDirectoryInfo(path string) (DirectoryInfo, error) {
	if !fs.mounted {
		return nil, fmt.Errorf("ISO not mounted")
	}

	// Normalize path
	path = fs.normalizePath(path)

	// Check cache
	if dir, exists := fs.dirCache[path]; exists {
		return &isoDirectoryInfo{
			name:     filepath.Base(path),
			fullPath: path,
			fs:       fs,
			dir:      dir,
		}, nil
	}

	// Read from UDF
	dir, err := fs.udfReader.ReadDirectory(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read directory: %w", err)
	}

	// Cache it
	fs.dirCache[path] = dir

	return &isoDirectoryInfo{
		name:     filepath.Base(path),
		fullPath: path,
		fs:       fs,
		dir:      dir,
	}, nil
}

// GetFileInfo returns information about a file in the ISO.
func (fs *ISOFileSystemImpl) GetFileInfo(path string) (FileInfo, error) {
	if !fs.mounted {
		return nil, fmt.Errorf("ISO not mounted")
	}

	// Normalize path
	path = fs.normalizePath(path)

	// Find file in UDF
	file, err := fs.udfReader.FindFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to find file: %w", err)
	}

	return &isoFileInfo{
		name:     filepath.Base(path),
		fullPath: path,
		fs:       fs,
		file:     file,
	}, nil
}

// IsISO returns true for ISO file system.
func (fs *ISOFileSystemImpl) IsISO() bool {
	return true
}

// normalizePath normalizes a path for UDF access
func (fs *ISOFileSystemImpl) normalizePath(p string) string {
	// Remove any leading slash variations
	p = strings.TrimPrefix(p, "./")
	p = strings.TrimPrefix(p, "/")

	// Ensure paths use forward slashes
	p = filepath.ToSlash(p)

	// Root is "/"
	if p == "" {
		return "/"
	}

	return "/" + p
}

// isoFileInfo implements FileInfo for files within an ISO.
type isoFileInfo struct {
	name     string
	fullPath string
	fs       *ISOFileSystemImpl
	file     *udf.File
}

func (f *isoFileInfo) Name() string {
	return f.name
}

func (f *isoFileInfo) FullName() string {
	return f.fullPath
}

func (f *isoFileInfo) Length() int64 {
	if f.file == nil {
		return 0
	}
	return f.file.Size()
}

func (f *isoFileInfo) Extension() string {
	if idx := strings.LastIndex(f.name, "."); idx >= 0 {
		return f.name[idx:]
	}
	return ""
}

func (f *isoFileInfo) IsDirectory() bool {
	return false
}

func (f *isoFileInfo) ModTime() time.Time {
	if f.file == nil {
		return time.Time{}
	}
	return f.file.ModTime()
}

func (f *isoFileInfo) OpenRead() (io.ReadCloser, error) {
	if f.file == nil {
		return nil, fmt.Errorf("file not initialized")
	}
	return f.file.Open()
}

// isoDirectoryInfo implements DirectoryInfo for directories within an ISO.
type isoDirectoryInfo struct {
	name     string
	fullPath string
	fs       *ISOFileSystemImpl
	dir      *udf.Directory
}

func (d *isoDirectoryInfo) Name() string {
	return d.name
}

func (d *isoDirectoryInfo) FullName() string {
	return d.fullPath
}

func (d *isoDirectoryInfo) GetFiles() ([]FileInfo, error) {
	if d.dir == nil {
		return nil, fmt.Errorf("directory not initialized")
	}

	udfFiles, err := d.dir.GetFiles()
	if err != nil {
		return nil, err
	}

	var files []FileInfo
	for _, udfFile := range udfFiles {
		files = append(files, &isoFileInfo{
			name:     udfFile.Name,
			fullPath: path.Join(d.fullPath, udfFile.Name),
			fs:       d.fs,
			file:     udfFile,
		})
	}

	return files, nil
}

func (d *isoDirectoryInfo) GetDirectories() ([]DirectoryInfo, error) {
	if d.dir == nil {
		return nil, fmt.Errorf("directory not initialized")
	}

	udfDirs, err := d.dir.GetDirectories()
	if err != nil {
		return nil, err
	}

	var dirs []DirectoryInfo
	for _, udfDir := range udfDirs {
		dirPath := path.Join(d.fullPath, udfDir.Name)
		d.fs.dirCache[dirPath] = udfDir

		dirs = append(dirs, &isoDirectoryInfo{
			name:     udfDir.Name,
			fullPath: dirPath,
			fs:       d.fs,
			dir:      udfDir,
		})
	}

	return dirs, nil
}

func (d *isoDirectoryInfo) GetFilesPattern(pattern string) ([]FileInfo, error) {
	files, err := d.GetFiles()
	if err != nil {
		return nil, err
	}

	var matches []FileInfo
	for _, file := range files {
		matched, err := filepath.Match(pattern, file.Name())
		if err != nil {
			return nil, err
		}
		if matched {
			matches = append(matches, file)
		}
	}

	return matches, nil
}

func (d *isoDirectoryInfo) GetDirectory(name string) (DirectoryInfo, error) {
	dirs, err := d.GetDirectories()
	if err != nil {
		return nil, err
	}

	for _, dir := range dirs {
		if strings.EqualFold(dir.Name(), name) {
			return dir, nil
		}
	}

	return nil, fmt.Errorf("directory not found: %s", name)
}

func (d *isoDirectoryInfo) GetFile(name string) (FileInfo, error) {
	files, err := d.GetFiles()
	if err != nil {
		return nil, err
	}

	for _, file := range files {
		if strings.EqualFold(file.Name(), name) {
			return file, nil
		}
	}

	return nil, fmt.Errorf("file not found: %s", name)
}

func (d *isoDirectoryInfo) Exists() bool {
	return d.dir != nil
}
