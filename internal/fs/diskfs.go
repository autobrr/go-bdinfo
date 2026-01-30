package fs

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// DiskFileSystem implements FileSystem for regular disk access.
type DiskFileSystem struct{}

// NewDiskFileSystem creates a new disk-based file system.
func NewDiskFileSystem() FileSystem {
	return &DiskFileSystem{}
}

// GetDirectoryInfo returns information about a directory on disk.
func (fs *DiskFileSystem) GetDirectoryInfo(path string) (DirectoryInfo, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("%s is not a directory", path)
	}
	return &diskDirectoryInfo{path: path}, nil
}

// GetFileInfo returns information about a file on disk.
func (fs *DiskFileSystem) GetFileInfo(path string) (FileInfo, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	if info.IsDir() {
		return nil, fmt.Errorf("%s is a directory, not a file", path)
	}
	return &diskFileInfo{
		path: path,
		info: info,
	}, nil
}

// IsISO returns false for disk file system.
func (fs *DiskFileSystem) IsISO() bool {
	return false
}

// diskFileInfo implements FileInfo for regular files.
type diskFileInfo struct {
	path string
	info os.FileInfo
}

func (f *diskFileInfo) Name() string {
	return f.info.Name()
}

func (f *diskFileInfo) FullName() string {
	return f.path
}

func (f *diskFileInfo) Length() int64 {
	return f.info.Size()
}

func (f *diskFileInfo) Extension() string {
	return strings.ToLower(filepath.Ext(f.path))
}

func (f *diskFileInfo) IsDirectory() bool {
	return f.info.IsDir()
}

func (f *diskFileInfo) ModTime() time.Time {
	return f.info.ModTime()
}

func (f *diskFileInfo) OpenRead() (io.ReadCloser, error) {
	return os.Open(f.path)
}

// diskDirectoryInfo implements DirectoryInfo for regular directories.
type diskDirectoryInfo struct {
	path string
}

func (d *diskDirectoryInfo) Name() string {
	return filepath.Base(d.path)
}

func (d *diskDirectoryInfo) FullName() string {
	return d.path
}

func (d *diskDirectoryInfo) GetFiles() ([]FileInfo, error) {
	entries, err := os.ReadDir(d.path)
	if err != nil {
		return nil, err
	}
	
	var files []FileInfo
	for _, entry := range entries {
		if !entry.IsDir() {
			info, err := entry.Info()
			if err != nil {
				continue
			}
			files = append(files, &diskFileInfo{
				path: filepath.Join(d.path, entry.Name()),
				info: info,
			})
		}
	}
	return files, nil
}

func (d *diskDirectoryInfo) GetDirectories() ([]DirectoryInfo, error) {
	entries, err := os.ReadDir(d.path)
	if err != nil {
		return nil, err
	}
	
	var dirs []DirectoryInfo
	for _, entry := range entries {
		if entry.IsDir() {
			dirs = append(dirs, &diskDirectoryInfo{
				path: filepath.Join(d.path, entry.Name()),
			})
		}
	}
	return dirs, nil
}

func (d *diskDirectoryInfo) GetFilesPattern(pattern string) ([]FileInfo, error) {
	entries, err := os.ReadDir(d.path)
	if err != nil {
		return nil, err
	}
	
	var files []FileInfo
	for _, entry := range entries {
		if !entry.IsDir() {
			matched, err := filepath.Match(pattern, entry.Name())
			if err != nil {
				return nil, err
			}
			if matched {
				info, err := entry.Info()
				if err != nil {
					continue
				}
				files = append(files, &diskFileInfo{
					path: filepath.Join(d.path, entry.Name()),
					info: info,
				})
			}
		}
	}
	return files, nil
}

func (d *diskDirectoryInfo) GetDirectory(name string) (DirectoryInfo, error) {
	path := filepath.Join(d.path, name)
	info, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("%s is not a directory", name)
	}
	return &diskDirectoryInfo{path: path}, nil
}

func (d *diskDirectoryInfo) GetFile(name string) (FileInfo, error) {
	path := filepath.Join(d.path, name)
	info, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	if info.IsDir() {
		return nil, fmt.Errorf("%s is a directory, not a file", name)
	}
	return &diskFileInfo{
		path: path,
		info: info,
	}, nil
}

func (d *diskDirectoryInfo) Exists() bool {
	_, err := os.Stat(d.path)
	return err == nil
}