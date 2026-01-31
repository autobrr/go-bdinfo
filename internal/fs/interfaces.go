package fs

import (
	"io"
	"time"
)

// FileInfo represents information about a file.
// This interface abstracts file operations for both regular files and ISO files.
type FileInfo interface {
	// Name returns the base name of the file.
	Name() string

	// FullName returns the full path of the file.
	FullName() string

	// Length returns the size of the file in bytes.
	Length() int64

	// Extension returns the file extension (including the dot).
	Extension() string

	// IsDirectory returns true if this is a directory.
	IsDirectory() bool

	// ModTime returns the modification time.
	ModTime() time.Time

	// OpenRead opens the file for reading.
	OpenRead() (io.ReadCloser, error)
}

// DirectoryInfo represents information about a directory.
// This interface abstracts directory operations for both regular directories and ISO directories.
type DirectoryInfo interface {
	// Name returns the base name of the directory.
	Name() string

	// FullName returns the full path of the directory.
	FullName() string

	// GetFiles returns all files in the directory.
	GetFiles() ([]FileInfo, error)

	// GetDirectories returns all subdirectories.
	GetDirectories() ([]DirectoryInfo, error)

	// GetFiles returns files matching the given pattern (e.g., "*.mpls").
	GetFilesPattern(pattern string) ([]FileInfo, error)

	// GetDirectory returns a subdirectory by name.
	GetDirectory(name string) (DirectoryInfo, error)

	// GetFile returns a file by name.
	GetFile(name string) (FileInfo, error)

	// Exists returns true if the directory exists.
	Exists() bool
}

// FileSystem provides an abstraction over file system operations.
// This allows us to work with both regular file systems and ISO files.
type FileSystem interface {
	// GetDirectoryInfo returns information about a directory.
	GetDirectoryInfo(path string) (DirectoryInfo, error)

	// GetFileInfo returns information about a file.
	GetFileInfo(path string) (FileInfo, error)

	// IsISO returns true if this is an ISO file system.
	IsISO() bool
}

// ISOFileSystem represents a file system within an ISO image.
type ISOFileSystem interface {
	FileSystem

	// Mount opens the ISO file and prepares it for reading.
	Mount(isoPath string) error

	// Unmount closes the ISO file.
	Unmount() error

	// GetVolumeLabel returns the volume label of the ISO.
	GetVolumeLabel() string
}
