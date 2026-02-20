package bdrom

import (
	"bytes"
	"encoding/xml"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"

	"github.com/autobrr/go-bdinfo/internal/fs"
	"github.com/autobrr/go-bdinfo/internal/settings"
	"github.com/autobrr/go-bdinfo/internal/stream"
	"github.com/autobrr/go-bdinfo/internal/util"
)

type BDROM struct {
	Path              string
	Settings          settings.Settings
	fileSystem        fs.FileSystem
	rootDirectory     fs.DirectoryInfo
	bdmvDirectory     fs.DirectoryInfo
	clipinfDirectory  fs.DirectoryInfo
	playlistDirectory fs.DirectoryInfo
	streamDirectory   fs.DirectoryInfo
	ssifDirectory     fs.DirectoryInfo
	metaDirectory     fs.DirectoryInfo
	bdjoDirectory     fs.DirectoryInfo
	snpDirectory      fs.DirectoryInfo

	DirectoryRoot     string
	DirectoryBDMV     string
	DirectoryBDJO     string
	DirectoryCLIPINF  string
	DirectoryPLAYLIST string
	DirectorySNP      string
	DirectorySSIF     string
	DirectorySTREAM   string
	DirectoryMeta     string

	VolumeLabel string
	DiscTitle   string
	Size        uint64
	IsBDPlus    bool
	IsBDJava    bool
	IsDBOX      bool
	IsPSP       bool
	Is3D        bool
	Is50Hz      bool
	IsUHD       bool

	PlaylistFiles    map[string]*PlaylistFile
	PlaylistOrder    []string
	StreamClipFiles  map[string]*StreamClipFile
	StreamFiles      map[string]*StreamFile
	InterleavedFiles map[string]*InterleavedFile

	cleanup func()
}

type ScanResult struct {
	ScanError  error
	FileErrors map[string]error
}

const maxScanWorkers = 8

func scanWorkerLimit(total int, totalBytes uint64) int {
	if override := os.Getenv("BDINFO_WORKERS"); override != "" {
		if parsed, err := strconv.Atoi(override); err == nil && parsed > 0 {
			return clampWorkers(parsed, total)
		}
	}
	if totalBytes > 0 {
		// Stream scans are storage-bound in this workload. Sequential single-worker reads
		// avoid seek-thrash and improve throughput on both ISO and folder sources.
		return clampWorkers(1, total)
	}
	limit := tunedWorkerLimit(total, totalBytes)
	return clampWorkers(limit, total)
}

func clampWorkers(limit int, total int) int {
	if limit < 1 {
		limit = 1
	}
	cpu := max(runtime.NumCPU(), 1)
	maxWorkers := max(cpu-1, 1)
	if maxWorkers > maxScanWorkers {
		maxWorkers = maxScanWorkers
	}
	if limit > maxWorkers {
		limit = maxWorkers
	}
	if total > 0 && limit > total {
		limit = total
	}
	return limit
}

func tunedWorkerLimit(total int, totalBytes uint64) int {
	if total <= 1 {
		return 1
	}
	if totalBytes == 0 {
		switch {
		case total <= 4:
			return 2
		case total <= 8:
			return 3
		default:
			return 4
		}
	}
	sizeGiB := totalBytes / (1 << 30)
	switch {
	case sizeGiB >= 80:
		return 4
	case sizeGiB >= 50:
		return 3
	case sizeGiB >= 20:
		return 2
	default:
		return 1
	}
}

func runParallel[T any](items []T, limit int, fn func(T) error, onErr func(T, error)) {
	if len(items) == 0 {
		return
	}
	if limit < 1 {
		limit = 1
	}
	if limit > len(items) {
		limit = len(items)
	}
	sem := make(chan struct{}, limit)
	var wg sync.WaitGroup
	for _, item := range items {
		wg.Add(1)
		sem <- struct{}{}
		go func() {
			defer wg.Done()
			defer func() { <-sem }()
			if err := fn(item); err != nil && onErr != nil {
				onErr(item, err)
			}
		}()
	}
	wg.Wait()
}

func orderedStreamClipFiles(files map[string]*StreamClipFile) []*StreamClipFile {
	if len(files) == 0 {
		return nil
	}
	out := make([]*StreamClipFile, 0, len(files))
	for _, clip := range files {
		out = append(out, clip)
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].Name < out[j].Name
	})
	return out
}

func orderedStreamFiles(files map[string]*StreamFile) []*StreamFile {
	if len(files) == 0 {
		return nil
	}
	out := make([]*StreamFile, 0, len(files))
	for _, file := range files {
		out = append(out, file)
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].Name < out[j].Name
	})
	return out
}

func streamFilesTotalSize(streamFiles []*StreamFile) uint64 {
	var total uint64
	for _, streamFile := range streamFiles {
		if streamFile == nil {
			continue
		}
		if streamFile.Size > 0 {
			total += uint64(streamFile.Size)
		} else if streamFile.FileInfo != nil {
			total += uint64(streamFile.FileInfo.Length())
		}
	}
	return total
}

func orderedPlaylists(playlists map[string]*PlaylistFile, order []string) []*PlaylistFile {
	if len(playlists) == 0 {
		return nil
	}
	if len(order) == 0 {
		out := make([]*PlaylistFile, 0, len(playlists))
		for _, pl := range playlists {
			out = append(out, pl)
		}
		sort.Slice(out, func(i, j int) bool {
			return out[i].Name < out[j].Name
		})
		return out
	}
	out := make([]*PlaylistFile, 0, len(order))
	for _, name := range order {
		if pl, ok := playlists[name]; ok {
			out = append(out, pl)
		}
	}
	return out
}

func buildStreamPlaylistIndex(playlists []*PlaylistFile) map[*StreamFile][]*PlaylistFile {
	index := make(map[*StreamFile][]*PlaylistFile)
	for _, pl := range playlists {
		if pl == nil {
			continue
		}
		seen := make(map[*StreamFile]bool)
		for _, clip := range pl.StreamClips {
			if clip == nil || clip.StreamFile == nil {
				continue
			}
			if seen[clip.StreamFile] {
				continue
			}
			seen[clip.StreamFile] = true
			index[clip.StreamFile] = append(index[clip.StreamFile], pl)
		}
	}
	return index
}

func New(path string, settings settings.Settings) (*BDROM, error) {
	rootPath := path
	cleanup := func() {
		// No cleanup needed for regular directory access
	}
	fileSystem := fs.NewDiskFileSystem()
	volumeLabel := ""

	if strings.HasSuffix(strings.ToLower(path), ".iso") {
		isoFS := fs.NewISOFileSystem()
		if err := isoFS.Mount(path); err != nil {
			return nil, err
		}
		fileSystem = isoFS
		rootPath = "/"
		volumeLabel = isoFS.GetVolumeLabel()
		cleanup = func() { _ = isoFS.Unmount() }
	}

	rootDir, err := fileSystem.GetDirectoryInfo(rootPath)
	if err != nil {
		cleanup()
		return nil, err
	}

	bdmvDir, err := findBDMVDirectory(rootDir)
	if err != nil {
		cleanup()
		return nil, err
	}

	rom := &BDROM{
		Path:             path,
		Settings:         settings,
		fileSystem:       fileSystem,
		rootDirectory:    rootDir,
		bdmvDirectory:    bdmvDir,
		PlaylistFiles:    make(map[string]*PlaylistFile),
		PlaylistOrder:    make([]string, 0),
		StreamClipFiles:  make(map[string]*StreamClipFile),
		StreamFiles:      make(map[string]*StreamFile),
		InterleavedFiles: make(map[string]*InterleavedFile),
		cleanup:          cleanup,
	}

	rom.DirectoryRoot = rootDir.FullName()
	rom.DirectoryBDMV = bdmvDir.FullName()

	if dir, err := bdmvDir.GetDirectory("BDJO"); err == nil {
		rom.bdjoDirectory = dir
		rom.DirectoryBDJO = dir.FullName()
	}
	if dir, err := bdmvDir.GetDirectory("CLIPINF"); err == nil {
		rom.clipinfDirectory = dir
		rom.DirectoryCLIPINF = dir.FullName()
	}
	if dir, err := bdmvDir.GetDirectory("PLAYLIST"); err == nil {
		rom.playlistDirectory = dir
		rom.DirectoryPLAYLIST = dir.FullName()
	}
	if dir, err := bdmvDir.GetDirectory("STREAM"); err == nil {
		rom.streamDirectory = dir
		rom.DirectorySTREAM = dir.FullName()
		if ssifDir, err := dir.GetDirectory("SSIF"); err == nil {
			rom.ssifDirectory = ssifDir
			rom.DirectorySSIF = ssifDir.FullName()
		}
	}
	if dir, err := bdmvDir.GetDirectory("META"); err == nil {
		rom.metaDirectory = dir
		rom.DirectoryMeta = dir.FullName()
	}
	if dir, err := rootDir.GetDirectory("SNP"); err == nil {
		rom.snpDirectory = dir
		rom.DirectorySNP = dir.FullName()
	}

	if rom.clipinfDirectory == nil || rom.playlistDirectory == nil {
		rom.cleanup()
		return nil, fmt.Errorf("unable to locate BD structure")
	}

	if volumeLabel == "" {
		volumeLabel = filepath.Base(rom.DirectoryRoot)
	}
	rom.VolumeLabel = volumeLabel
	rom.Size = uint64(getDirectorySizeFS(rootDir))

	if indexFile, err := bdmvDir.GetFile("index.bdmv"); err == nil {
		if header, err := readFileHeader(indexFile, 8); err == nil && len(header) >= 8 {
			if string(header[:8]) == "INDX0300" {
				rom.IsUHD = true
			}
		}
	}

	rom.IsBDPlus = directoryExistsFS(rootDir, "BDSVM") ||
		directoryExistsFS(rootDir, "SLYVM") ||
		directoryExistsFS(rootDir, "ANYVM")

	if rom.bdjoDirectory != nil {
		if files, err := rom.bdjoDirectory.GetFiles(); err == nil && len(files) > 0 {
			rom.IsBDJava = true
		}
	}

	if rom.snpDirectory != nil {
		if files, err := rom.snpDirectory.GetFiles(); err == nil {
			for _, file := range files {
				if strings.HasSuffix(strings.ToLower(file.Name()), ".mnv") {
					rom.IsPSP = true
					break
				}
			}
		}
	}

	if rom.ssifDirectory != nil {
		if files, err := rom.ssifDirectory.GetFiles(); err == nil && len(files) > 0 {
			rom.Is3D = true
		}
	}

	if fileExistsFS(rootDir, "FilmIndex.xml") {
		rom.IsDBOX = true
	}

	rom.DiscTitle = readDiscTitleFS(rom.metaDirectory)

	if rom.playlistDirectory != nil {
		files, err := rom.playlistDirectory.GetFilesPattern("*.mpls")
		if err != nil || len(files) == 0 {
			files, err = rom.playlistDirectory.GetFilesPattern("*.MPLS")
		}
		if err == nil {
			for _, file := range files {
				pl := NewPlaylistFile(file, settings)
				rom.PlaylistFiles[pl.Name] = pl
				rom.PlaylistOrder = append(rom.PlaylistOrder, pl.Name)
			}
		}
	}

	if rom.streamDirectory != nil {
		files, err := rom.streamDirectory.GetFilesPattern("*.m2ts")
		if err != nil || len(files) == 0 {
			files, err = rom.streamDirectory.GetFilesPattern("*.M2TS")
		}
		if err == nil {
			for _, file := range files {
				sf := NewStreamFile(file)
				rom.StreamFiles[sf.Name] = sf
			}
		}
	}

	if rom.clipinfDirectory != nil {
		files, err := rom.clipinfDirectory.GetFilesPattern("*.clpi")
		if err != nil || len(files) == 0 {
			files, err = rom.clipinfDirectory.GetFilesPattern("*.CLPI")
		}
		if err == nil {
			for _, file := range files {
				cf := NewStreamClipFile(file)
				rom.StreamClipFiles[cf.Name] = cf
			}
		}
	}

	if rom.ssifDirectory != nil {
		files, err := rom.ssifDirectory.GetFilesPattern("*.ssif")
		if err != nil || len(files) == 0 {
			files, err = rom.ssifDirectory.GetFilesPattern("*.SSIF")
		}
		if err == nil {
			for _, file := range files {
				rom.InterleavedFiles[strings.ToUpper(file.Name())] = &InterleavedFile{FileInfo: file, Name: strings.ToUpper(file.Name()), Size: file.Length()}
			}
		}
	}

	return rom, nil
}

func (b *BDROM) Close() {
	if b.cleanup != nil {
		b.cleanup()
	}
}

func (b *BDROM) Scan() ScanResult {
	result := ScanResult{FileErrors: make(map[string]error)}
	var errMu sync.Mutex

	clipFiles := orderedStreamClipFiles(b.StreamClipFiles)
	runParallel(clipFiles, scanWorkerLimit(len(clipFiles), 0), func(clip *StreamClipFile) error {
		return clip.Scan()
	}, func(clip *StreamClipFile, err error) {
		errMu.Lock()
		result.FileErrors[clip.Name] = err
		errMu.Unlock()
	})

	for _, streamFile := range b.StreamFiles {
		ssifName := strings.ToUpper(strings.TrimSuffix(streamFile.Name, ".M2TS") + ".SSIF")
		if ssif, ok := b.InterleavedFiles[ssifName]; ok {
			streamFile.InterleavedFile = ssif
		}
	}

	playlists := orderedPlaylists(b.PlaylistFiles, b.PlaylistOrder)
	runParallel(playlists, scanWorkerLimit(len(playlists), 0), func(playlist *PlaylistFile) error {
		return playlist.Scan(b.StreamFiles, b.StreamClipFiles)
	}, func(playlist *PlaylistFile, err error) {
		errMu.Lock()
		result.FileErrors[playlist.Name] = err
		errMu.Unlock()
	})

	// scan stream files
	streamFiles := orderedStreamFiles(b.StreamFiles)
	streamPlaylists := buildStreamPlaylistIndex(playlists)
	filteredStreamFiles := streamFiles[:0]
	for _, streamFile := range streamFiles {
		if len(streamPlaylists[streamFile]) == 0 {
			continue
		}
		filteredStreamFiles = append(filteredStreamFiles, streamFile)
	}
	streamFiles = filteredStreamFiles
	streamBytes := streamFilesTotalSize(streamFiles)
	runParallel(streamFiles, scanWorkerLimit(len(streamFiles), streamBytes), func(streamFile *StreamFile) error {
		return streamFile.Scan(streamPlaylists[streamFile], false)
	}, func(streamFile *StreamFile, err error) {
		errMu.Lock()
		result.FileErrors[streamFile.Name] = err
		errMu.Unlock()
	})

	runParallel(playlists, scanWorkerLimit(len(playlists), 0), func(playlist *PlaylistFile) error {
		playlist.Initialize()
		return nil
	}, nil)

	for _, playlist := range playlists {
		if b.Is50Hz {
			continue
		}
		vidCount := len(playlist.VideoStreams)
		for _, vs := range playlist.VideoStreams {
			if vs.FrameRate() == stream.FrameRate25 || vs.FrameRate() == stream.FrameRate50 {
				b.Is50Hz = true
			}
			if vidCount > 1 && b.Is3D {
				if (vs.StreamType == stream.StreamTypeAVCVideo && playlist.MVCBaseViewR) ||
					(vs.StreamType == stream.StreamTypeMVCVideo && !playlist.MVCBaseViewR) {
					base := true
					vs.BaseView = &base
				} else if vs.StreamType == stream.StreamTypeAVCVideo || vs.StreamType == stream.StreamTypeMVCVideo {
					base := false
					vs.BaseView = &base
				}
			}
		}
	}

	return result
}

// ScanFull performs a full bitrate/diagnostics scan over stream files.
func (b *BDROM) ScanFull() ScanResult {
	result := ScanResult{FileErrors: make(map[string]error)}
	var errMu sync.Mutex

	playlists := orderedPlaylists(b.PlaylistFiles, b.PlaylistOrder)
	runParallel(playlists, scanWorkerLimit(len(playlists), 0), func(playlist *PlaylistFile) error {
		playlist.ClearBitrates()
		return nil
	}, nil)

	streamFiles := orderedStreamFiles(b.StreamFiles)
	streamPlaylists := buildStreamPlaylistIndex(playlists)
	filteredStreamFiles := streamFiles[:0]
	for _, streamFile := range streamFiles {
		if len(streamPlaylists[streamFile]) == 0 {
			continue
		}
		filteredStreamFiles = append(filteredStreamFiles, streamFile)
	}
	streamFiles = filteredStreamFiles
	streamBytes := streamFilesTotalSize(streamFiles)
	runParallel(streamFiles, scanWorkerLimit(len(streamFiles), streamBytes), func(streamFile *StreamFile) error {
		return streamFile.Scan(streamPlaylists[streamFile], true)
	}, func(streamFile *StreamFile, err error) {
		errMu.Lock()
		result.FileErrors[streamFile.Name] = err
		errMu.Unlock()
	})

	return result
}

func findBDMVDirectory(root fs.DirectoryInfo) (fs.DirectoryInfo, error) {
	if root == nil {
		return nil, fmt.Errorf("unable to locate BD structure")
	}
	if strings.EqualFold(root.Name(), "BDMV") {
		if _, err := root.GetDirectory("PLAYLIST"); err == nil {
			return root, nil
		}
		if _, err := root.GetDirectory("STREAM"); err == nil {
			return root, nil
		}
	}

	queue := []fs.DirectoryInfo{root}
	for len(queue) > 0 {
		dir := queue[0]
		queue = queue[1:]

		dirs, err := dir.GetDirectories()
		if err != nil {
			continue
		}
		for _, sub := range dirs {
			if strings.EqualFold(sub.Name(), "BDMV") {
				if _, err := sub.GetDirectory("PLAYLIST"); err == nil {
					return sub, nil
				}
				if _, err := sub.GetDirectory("STREAM"); err == nil {
					return sub, nil
				}
			}
			queue = append(queue, sub)
		}
	}

	return nil, fmt.Errorf("unable to locate BD structure")
}

func directoryExistsFS(root fs.DirectoryInfo, name string) bool {
	if root == nil {
		return false
	}
	_, err := root.GetDirectory(name)
	return err == nil
}

func fileExistsFS(root fs.DirectoryInfo, name string) bool {
	if root == nil {
		return false
	}
	_, err := root.GetFile(name)
	return err == nil
}

func getDirectorySizeFS(root fs.DirectoryInfo) int64 {
	if root == nil {
		return 0
	}
	var size int64
	queue := []fs.DirectoryInfo{root}
	for len(queue) > 0 {
		dir := queue[0]
		queue = queue[1:]

		files, err := dir.GetFiles()
		if err == nil {
			for _, file := range files {
				if strings.EqualFold(path.Ext(file.Name()), ".ssif") {
					continue
				}
				size += file.Length()
			}
		}
		subdirs, err := dir.GetDirectories()
		if err != nil {
			continue
		}
		for _, sub := range subdirs {
			if strings.EqualFold(path.Ext(sub.Name()), ".ssif") {
				continue
			}
			queue = append(queue, sub)
		}
	}
	return size
}

func readFileHeader(file fs.FileInfo, length int) ([]byte, error) {
	if file == nil || length <= 0 {
		return nil, fmt.Errorf("invalid file")
	}
	reader, err := file.OpenRead()
	if err != nil {
		return nil, err
	}
	defer reader.Close()
	buf := make([]byte, length)
	n, err := reader.Read(buf)
	if err != nil && n == 0 {
		return nil, err
	}
	return buf[:n], nil
}

func readDiscTitleFS(metaDir fs.DirectoryInfo) string {
	if metaDir == nil {
		return ""
	}
	file, ok := findFileCaseInsensitive(metaDir, "bdmt_eng.xml")
	if !ok {
		return ""
	}
	reader, err := file.OpenRead()
	if err != nil {
		return ""
	}
	defer reader.Close()
	data, err := io.ReadAll(reader)
	if err != nil {
		return ""
	}
	decoder := xml.NewDecoder(bytes.NewReader(data))
	inTitle := false
	inName := false
	var nameBuilder strings.Builder
	for {
		tok, err := decoder.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return ""
		}
		switch t := tok.(type) {
		case xml.StartElement:
			switch t.Name.Local {
			case "title":
				inTitle = true
			case "name":
				if inTitle {
					inName = true
					nameBuilder.Reset()
				}
			}
		case xml.EndElement:
			switch t.Name.Local {
			case "name":
				if inTitle && inName {
					name := strings.TrimSpace(nameBuilder.String())
					if strings.EqualFold(name, "blu-ray") {
						return ""
					}
					if name != "" {
						return name
					}
				}
				inName = false
			case "title":
				inTitle = false
			}
		case xml.CharData:
			if inTitle && inName {
				nameBuilder.Write(t)
			}
		}
	}
	return ""
}

func findFileCaseInsensitive(root fs.DirectoryInfo, target string) (fs.FileInfo, bool) {
	queue := []fs.DirectoryInfo{root}
	for len(queue) > 0 {
		dir := queue[0]
		queue = queue[1:]

		files, err := dir.GetFiles()
		if err == nil {
			for _, file := range files {
				if strings.EqualFold(file.Name(), target) {
					return file, true
				}
			}
		}
		dirs, err := dir.GetDirectories()
		if err != nil {
			continue
		}
		queue = append(queue, dirs...)
	}
	return nil, false
}

func (b *BDROM) FormatSize() string {
	return util.FormatFileSize(float64(b.Size), true)
}
