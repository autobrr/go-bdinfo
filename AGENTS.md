# go-bdinfo Project Documentation

## Overview
This project is a Go implementation of BDInfo, a tool for analyzing Blu-ray disc structures. The original is written in C# and we're creating a pure Go version without CGO dependencies.

## Goal: accurate output
Official BDInfo was the parity oracle during the port, not the specification. Where BDInfo is correct, match its report text byte for byte. Where BDInfo contradicts the format specification (MPEG-TS, MPLS, CLPI, codec bitstreams, PGS), follow the specification and record the delta under [Known Divergences](#known-divergences-bdinfo-bugs-fixed-in-go).

The C# source is the reference for the port: parsing algorithms, codec analysis, report text layout, edge cases. The format specifications are the reference for correctness.

## Original BDInfo Features
- Scans Blu-ray discs (Full HD, Ultra HD, 3D) from folders or ISO files
- Analyzes video, audio, and subtitle streams
- Detects codecs, bitrates, and technical specifications
- Generates detailed reports in various formats
- Supports BD+, BD-Java, and other disc features
- Includes BDExtractor for ISO extraction

## Architecture Mapping (C# to Go)

### Core Components

#### 1. BDROM (BDCommon/rom/BDROM.cs → pkg/bdrom/)
- **Purpose**: Main class representing a Blu-ray disc
- **Key Methods**:
  - `Scan()`: Discovers and analyzes disc structure
  - `ScanStreamFiles()`: Analyzes M2TS files
  - `ScanPlaylistFiles()`: Processes MPLS files
- **Go Implementation**: 
  - Use interfaces for file system abstraction
  - Implement parallel scanning with goroutines
  - Replace events with channels for progress reporting

#### 2. Stream Parsing (TSStreamFile.cs → pkg/stream/)
- **Transport Stream Parser**: 
  - Reads 192-byte packets (188 + 4 timestamp)
  - Extracts PAT/PMT tables
  - Identifies elementary streams
- **Key Algorithms**:
  - Sync byte detection (0x47)
  - PID filtering
  - PES packet assembly
  - PCR timestamp extraction

#### 3. Codec Analyzers (TSCodec*.cs → pkg/codec/)
Each codec has specific parsing logic:
- **H.264/AVC**: NAL unit parsing, SPS/PPS extraction
- **H.265/HEVC**: VPS/SPS/PPS, HDR metadata
- **DTS/DTS-HD**: Sync word detection (0x7FFE8001), channel config
- **AC-3**: Sync word (0x0B77), bitrate tables
- **TrueHD**: Major sync detection, Atmos flags

#### 4. Playlist Files (TSPlaylistFile.cs → pkg/bdrom/playlist.go)
- **MPLS Format**: Binary format with header "MPLS0100/0200/0300"
- **Key Structures**:
  - PlayList section: clip references
  - PlayItem section: stream selections
  - STN_table: stream attributes

#### 5. Clip Info Files (TSStreamClipFile.cs → pkg/bdrom/clipinfo.go)
- **CLPI Format**: Contains pre-analyzed stream info
- **Sections**:
  - ClipInfo: basic metadata
  - Sequence Info: stream properties
  - Program Info: stream PIDs and types

### Binary Parsing Utilities

#### TSStreamBuffer (→ pkg/buffer/bitreader.go)
Critical for codec parsing:
```go
type BitReader struct {
    data []byte
    pos  int
    bits int
}

// Key methods needed:
// - ReadBits(n int) uint64
// - ReadByte() byte
// - ReadUInt16() uint16
// - ReadUInt32() uint32
// - ReadExpGolomb() int (for H.264/H.265)
// - Skip(n int)
// - BytesLeft() int
```

### File System Abstraction

#### Interfaces (IO/*.cs → pkg/fs/)
```go
type FileInfo interface {
    Name() string
    FullName() string
    Length() int64
    Extension() string
    OpenRead() (io.ReadCloser, error)
}

type DirectoryInfo interface {
    Name() string
    FullName() string
    GetFiles() []FileInfo
    GetDirectories() []DirectoryInfo
    GetFiles(pattern string) []FileInfo
}
```

### ISO Support
- Original uses DiscUtils.Udf for ISO reading
- Go alternatives:
  - Implement minimal UDF reader
  - Use existing Go ISO libraries
  - Focus on BDMV structure navigation

## Key Algorithms and Data Structures

### 1. Transport Stream Demuxing
```
1. Read 192-byte packets
2. Check sync byte (0x47)
3. Extract PID from header
4. Track continuity counter
5. Assembly PES packets
6. Parse PES headers for PTS/DTS
```

### 2. Bitrate Calculation
```
For each stream:
- Count packets
- Track first/last PCR timestamps
- Calculate: bitrate = (bytes * 8) / duration
```

### 3. Video Format Detection
- **50Hz Detection**: Check frame rates (25, 50)
- **3D Detection**: Presence of SSIF files, MVC codec
- **HDR Detection**: Parse HEVC SEI messages
- **4K Detection**: Resolution >= 3840x2160

### 4. Stream Ordering
Streams sorted by:
1. Type (video, audio, graphics, text)
2. Language code
3. Codec priority
4. PID number

## Implementation Guidelines

### Error Handling
- Use Go's error interface consistently
- Wrap errors with context
- Continue scanning on non-fatal errors
- Report warnings via structured logging

### Performance Considerations
- Buffer I/O operations (use bufio)
- Parallel stream scanning
- Lazy loading for large files
- Cache parsed data when possible

### Testing Strategy
- Unit tests for each codec parser
- Test data: Create minimal valid structures
- Integration tests with real disc images
- Benchmark critical paths

## Progress Tracking

See [plan.md](plan.md) for detailed implementation plan and remaining work.

### Completed ✓
- [x] Project structure and setup
- [x] BitReader binary parsing utilities
- [x] File system abstractions
- [x] Basic BDROM structure
- [x] Stream type definitions
- [x] CLI interface
- [x] Basic report generation
- [x] Documentation (README, CLAUDE.md, plan.md)

### In Progress 🚧
- [x] M2TS transport stream parser - PAT/PMT parsing implemented and working
- [x] BBCode report format - Fully implemented with CLI --format flag
- [x] Codec analyzer integration with transport stream parser
  - ✓ Connected stream detection to codec analysis
  - ✓ Accumulating stream data for codec analyzers  
  - ✓ Basic codec detection working (H.264, TrueHD, E-AC-3)
  - ✓ Fixed stream file lookup (uppercase names)
  - ✓ Implemented seamless angle playlist duration calculation
  - ✓ Fixed language preservation from MPLS data
  - ✓ Fixed frame rate preservation from MPLS data
- [x] Performance optimization
  - ✓ Added buffered I/O (1MB buffer) for transport stream reading
  - ✓ Limited M2TS scan to 100MB (sufficient for codec detection)
  - ✓ Reduced codec data accumulation from 5MB to 1MB per stream
  - ✓ Added early termination when sufficient codec data collected
- [x] **COMPLETED**: Final integration and testing
  - ✓ Created comprehensive integration test suite
  - ✓ All report formats verified working (JSON, CSV, BBCode, XML)
  - ✓ Codec detection fully functional
  - ✓ Language and frame rate detection operational
  - ✓ Seamless angle playlist handling correct
  - ✓ Performance metrics acceptable
- [x] **MAJOR BREAKTHROUGH**: Fixed critical stream property bug
  - ✓ Identified root cause: codec analysis results were being overwritten during playlist merging
  - ✓ Fixed stream merging logic in `pkg/bdrom/playlist.go` to preserve analyzed properties
  - ✓ Resolution, frame rate, profile/level detection now working correctly
  - ✓ Should now display proper "1080p / 23.976 fps / High Profile 4.1" format

### Not Started ❌
- [ ] Stream diagnostics
- [ ] BDExtractor tool

### Notes
- All major codec analyzers have been implemented (H.264, H.265, AC-3, DTS, TrueHD, LPCM, AAC, PGS, MPEG-2, VC-1)
- ISO/UDF support has basic implementation but needs more work for non-standard layouts
- Transport stream parser is functional but only basic stream type detection is working
- All report formats are implemented and accessible via --format flag

## Common Pitfalls
1. **Endianness**: BD formats use big-endian
2. **String Encoding**: Use ISO-8859-1 for most text
3. **Packet Alignment**: TS packets may have 4-byte timestamp prefix
4. **SSIF Handling**: Interleaved 3D files need special processing
5. **Large Files**: Some M2TS files can be 40GB+

## Useful Constants
- TS Sync Byte: 0x47
- TS Packet Size: 188 (192 with timestamp)
- PAT PID: 0x0000
- PMT PIDs: Usually 0x0100-0x0FFF
- Video PIDs: Often 0x1011
- Audio PIDs: Often 0x1100-0x11FF

## Reference Materials
- Blu-ray Disc Association specs (when available)
- ISO/IEC 13818-1 (MPEG-2 Systems)
- ITU-T H.264/H.265 specifications
- ATSC A/52 (AC-3) specification
- Patent-free codec documentation

## Development Workflow
1. Check the original C# implementation in `~/github/oss/BDInfo-src/BDInfo.Core/BDCommon/rom/`
2. Write tests before implementation
3. Use standard Go conventions
4. Document codec-specific quirks
5. **Debug Tools**: Name temporary debug commands with prefixes like `test`, `debug`, or `check` (e.g., `cmd/teststream/`, `cmd/debugfids/`) so they can be easily identified and deleted later

## Parity Loop (Official BDInfo)
Report text identical to official BDInfo except the Known Divergences. Loop-to-done: run parity checks after changes, classify every diff line as a Go bug, a BDInfo bug, or a known divergence, fix the Go bugs, and land regression tests.

### Official Binary + Source
- Official Linux BDInfo binary (workspace convention): `~/github/oss/bdinfo-official/bdinfo_linux_v2.0.5_extracted/BDInfo`
- C# reference source: `~/github/oss/BDInfo-src/BDInfo.Core/BDCommon/rom/` (clone UniqProject/BDInfo if missing)

### Known Divergences (BDInfo bugs fixed in Go)
- PGS descriptions (`1920x1080 / N Captions`): the official Linux CLI leaves the cell empty because only the GUI runs `FormMain.UpdateSubtitleChapterCount`. Go ports `TSCodecPGS.Scan` (`internal/codec/pgs.go`) and the GUI sum (`PlaylistFile.UpdateGraphicsCaptions`) so the CLI report carries the value.
- PGS PCS cropping fields: BDInfo reads the 8-byte cropping rectangle on every composition object; the PGS format carries it only when `object_cropped_flag` (0x80) is set. Go reads it conditionally (`internal/codec/pgs.go`), so caption counts can differ on multi-object compositions.

### Output Quirks To Match (Gotchas)
- Hidden-tracks note: official inserts `\\n\\r\\n` before `(*) Indicates included stream hidden by this playlist.` when `playlist.HasHiddenTracks` is true. See `internal/report/report.go`.
- Chapter stats: official `Avg Frame Size` depends on per-transfer `StreamTag` from codec scan; do not default missing tags to `"I"`. Tag parse lives in `internal/bdrom/streamfile.go` (ported from `TSCodecAVC.cs`, `TSCodecMPEG2.cs`, `TSCodecVC1.cs`).
- Stream Diagnostics timing: official uses `clip.StreamFile.Length` (TSStreamFile.Length), which is DTS-derived and stays `0` unless at least 2 DTS-bearing timestamps are observed. Do not seed `StreamFile.Length` from playlist clip length (tiny/partial captures differ). See `internal/bdrom/streamfile.go`.
- HEVC chapter stats: official HEVC tag selection depends on init state and transfer size.
  - Uninitialized: keep scanning; last slice overwrites earlier tags (can become null). Buffer cap is effectively 5MB (`TSStreamBuffer`).
  - Initialized: stop at first non-null tag.
  - Go impl: `internal/codec/hevc_tag.go` + `internal/bdrom/streamfile.go` (5MB pre-init buffer, shrink after SPS). Test: `internal/codec/hevc_tag_test.go`.

### Quick Manual Parity Check (Report Text)
Sample 1-2 discs, never the full dataset.

```bash
off=~/github/oss/bdinfo-official/bdinfo_linux_v2.0.5_extracted/BDInfo
disc=/mnt/storage/torrents/Network.1976.1080p.USA.Blu-ray.AVC.LPCM.1.0-TMT
out=/tmp/bdinfo-parity
mkdir -p "$out"

"$off" -p "$disc" -o "$out/official.txt"
go run ./cmd/bdinfo -p "$disc" -o "$out/ours.txt"

diff -u --text "$out/official.txt" "$out/ours.txt"
```

Notes:
- ISO/UDF: BD-ROM ISOs commonly use a metadata partition map and multi-extent files; UDF reads must be concurrency-safe (use `ReadAt`-based access, no shared `Seek`).
- Speed loop: always measure official vs ours on the same sample path and compare wall time with exact command logs.
- Current perf policy: stream scans default to 1 worker (override with `BDINFO_WORKERS`) to avoid seek thrash on this storage profile.
- Harness: `scripts/speed_parity_loop.sh --disc "<disc-or-iso>" --reps 3` (matched toggles, per-rep parity check, median ratio).
- Diagnostics parity loop: derive stream diagnostics order from PMT stream order probe (`detectPMTStreamOrder`) with scan/CLPI fallback; verify on both anchors:
  - Network UHD (`00007/00009` hidden DV ordering)
  - Excalibur UHD (`00004` DV + audio/PGS ordering)
- Playlist same-language ordering parity: for English audio/graphics/text streams of same type, keep PID ascending (regression anchor: `The.Man.Who.Wasnt.There...`, 39.435 kbps subtitle before 68.796 kbps).
- Perf hotspot loop: if Network-like discs regress, check `internal/bdrom/streamfile.go` clip-target matching path first (active target cursor), then re-run harness.
- Sample cadence: smoke with `--reps 1` on ISO + Static + Network, then `--reps 3` on the regressing sample.
- Debug helper: `go run ./cmd/debugudf -iso "<path>.iso"` (lists key dirs/files, sanity-checks headers/sizes).

### Slow Oracle Test (Fuzzy Normalized)
Test: `internal/parity/bdinfo_parity_test.go` (gated; normalizes line endings/trailing whitespace).
It forces both official + go-bdinfo to `internal/settings.Default(...)` toggles for stable comparisons.

Env vars:
- `BDINFO_PARITY=1` enable
- `BDINFO_PARITY_DISC=/path/to/disc`
- `BDINFO_OFFICIAL_BIN=/path/to/official/BDInfo` (optional)
- `BDINFO_OFFICIAL_REPORT=/path/to/official.txt` (optional; skips running official binary)

Run:
```sh
BDINFO_PARITY=1 \
BDINFO_PARITY_DISC=/mnt/storage/torrents/Network.1976.1080p.USA.Blu-ray.AVC.LPCM.1.0-TMT \
BDINFO_OFFICIAL_REPORT=/tmp/bdinfo-parity/official.txt \
go test ./internal/parity -run TestParity_OfficialBDInfo_ReportText -count=1
```

## Fuzzing (Go Native)
Go fuzz targets live in `*_fuzz_test.go` and only run when invoked with `-fuzz=...`.

Examples:
```sh
go test ./internal/bdrom -run=^$ -fuzz=FuzzStreamClipFileScan -fuzztime=30s
go test ./internal/buffer -run=^$ -fuzz=FuzzBitReader -fuzztime=30s
go test ./internal/codec -run=^$ -fuzz=FuzzHEVCFrameTagFromTransfer -fuzztime=30s
```

## C# Source Reference Structure
Repo: `~/github/oss/BDInfo-src/`
- `BDInfo.Core/BDCommon/rom/`: Core parsing logic
  - `TSCodec*.cs`: Codec analyzers (AC3, DTS, AVC, HEVC, MPEG2, VC-1, etc.)
  - `TSStreamFile.cs`: Transport stream parser
  - `TSPlaylistFile.cs`: MPLS playlist parser
  - `TSStreamClipFile.cs`: CLPI clip info parser
  - `BDROM.cs`: Main disc structure
  - `LanguageCodes.cs`: ISO 639-2 language mappings

## Agent skills

### Issue tracker

Issues live in this repo's GitHub Issues (`autobrr/go-bdinfo`), managed with the `gh` CLI. See `docs/agents/issue-tracker.md`.

### Triage labels

Default canonical vocabulary — `needs-triage`, `needs-info`, `ready-for-agent`, `ready-for-human`, `wontfix`. See `docs/agents/triage-labels.md`.

### Domain docs

Single-context — `CONTEXT.md` and `docs/adr/` at the repo root, created lazily. See `docs/agents/domain.md`.
