# go-bdinfo

Pure Go port of BDInfo (C#), a Blu-ray disc analysis tool. No CGO.

## Goal: accurate output
Official BDInfo was the parity oracle during the port, not the specification. Where BDInfo is correct, match its report text byte for byte. Where BDInfo contradicts the format specification (MPEG-TS, MPLS, CLPI, codec bitstreams, PGS), follow the specification and record the delta under [Known Divergences](#known-divergences-bdinfo-bugs-fixed-in-go). The C# source is the reference for algorithms and report layout; the format specifications are the reference for correctness.

## Layout (C# to Go)
- `BDROM.cs`, `TSPlaylistFile.cs`, `TSStreamClipFile.cs`, `TSStreamFile.cs` → `internal/bdrom/` (`bdrom.go`, `playlist.go`, `clipinfo.go`, `streamfile.go`)
- `TSCodec*.cs` → `internal/codec/`
- `TSStream*.cs` stream types → `internal/stream/`
- `TSStreamBuffer.cs` → `internal/buffer/`
- `IO/*.cs` and DiscUtils.Udf → `internal/fs/` (`udf/` is the ISO reader; use `ReadAt`, no shared `Seek`, reads run concurrently)
- Report writers → `internal/report/`
- Library API → `pkg/bdinfo/`; CLI → `cmd/bdinfo/`

C# source, local clones: `~/github/oss/BDInfo-dotnetcorecorner/BDInfo.Core/BDCommon/rom/` (v2.0.5, the Linux CLI the reports come from) and `~/github/oss/BDInfo-src/BDInfo/BDROM/` (UniqProject GUI, has `FormMain.cs`). Official Linux binary and the disc library live on the media host: `root@media:/root/github/oss/bdinfo-official/bdinfo_linux_v2.0.5_extracted/BDInfo` and `/mnt/storage/torrents` (read-only). No Go toolchain there: cross-build with `GOOS=linux GOARCH=amd64 CGO_ENABLED=0` and `scp` the binary.

## Development Workflow
1. Read the C# implementation before porting or fixing a parser.
2. Write the regression test before the fix.
3. Document codec-specific quirks in the code.
4. Name temporary debug commands `cmd/test*`, `cmd/debug*`, or `cmd/check*` so they are easy to find and delete. `cmd/debugudf` is the kept one: `go run ./cmd/debugudf -iso "<path>.iso"` lists key dirs and files and sanity-checks headers and sizes.
5. Stream scans default to 1 worker (`BDINFO_WORKERS` overrides) to avoid seek thrash on the media host storage.

## Parity Loop (Official BDInfo)
Run a parity check after every parser or report change. Classify every diff line as a Go bug, a BDInfo bug, or a known divergence.

### Known Divergences (BDInfo bugs fixed in Go)
- PGS descriptions (`1920x1080 / N Captions`): the official Linux CLI leaves the cell empty because only the GUI runs `FormMain.UpdateSubtitleChapterCount`. Go ports `TSCodecPGS.Scan` (`internal/codec/pgs.go`) and the GUI sum (`PlaylistFile.UpdateGraphicsCaptions`) so the CLI report carries the value.
- PGS PCS cropping fields: BDInfo reads the 8-byte cropping rectangle on every composition object; the PGS format carries it only when `object_cropped_flag` (0x80) is set. Go reads it conditionally (`internal/codec/pgs.go`), so caption counts can differ on multi-object compositions.
- E-AC-3 dependent frames: `TSCodecAC3.Scan` parses one frame per call, stops as soon as the stream is initialized, and re-clones the core on every dependent frame. It only finds JOC/Atmos because its EMDF scan runs past the frame end into the next frame. Go (`internal/codec/ac3.go`) keeps parsing inside frame bounds, walks every consecutive dependent (strmtyp 1) frame after the independent frame, snapshots the embedded core once, and sums the dependent bitrates and channel maps onto the stream.

### Output Quirks To Match (Gotchas)
- Hidden-tracks note: official prefixes `(*) Indicates included stream hidden by this playlist.` with a bare `\r\n` in an otherwise LF report when `playlist.HasHiddenTracks` is true. See `internal/report/report.go`.
- Chapter stats: official `Avg Frame Size` depends on per-transfer `StreamTag` from codec scan; do not default missing tags to `"I"`. Tag parse lives in `internal/bdrom/streamfile.go` (ported from `TSCodecAVC.cs`, `TSCodecMPEG2.cs`, `TSCodecVC1.cs`).
- Stream Diagnostics timing: official uses `clip.StreamFile.Length` (TSStreamFile.Length), which is DTS-derived and stays `0` unless at least 2 DTS-bearing timestamps are observed. Do not seed `StreamFile.Length` from playlist clip length (tiny/partial captures differ). See `internal/bdrom/streamfile.go`.
- HEVC chapter stats: official HEVC tag selection depends on init state and transfer size.
  - Uninitialized: keep scanning; last slice overwrites earlier tags (can become null). Buffer cap is effectively 5MB (`TSStreamBuffer`).
  - Initialized: stop at first non-null tag.
  - Go impl: `internal/codec/hevc_tag.go` + `internal/bdrom/streamfile.go` (5MB pre-init buffer, shrink after SPS). Test: `internal/codec/hevc_tag_test.go`.
- Stream diagnostics order: PMT stream order probe (`detectPMTStreamOrder`) with scan/CLPI fallback. Anchors: Network UHD (`00007/00009` hidden DV ordering), Excalibur UHD (`00004` DV + audio/PGS ordering).
- Playlist same-language ordering: English audio/graphics/text streams of the same type keep PID ascending. Anchor: `The.Man.Who.Wasnt.There...`, 39.435 kbps subtitle before 68.796 kbps.

### Quick Manual Parity Check (Report Text)
Byte-exact diff, run on the media host. Sample 1-2 discs, never the full dataset.

```bash
off=/root/github/oss/bdinfo-official/bdinfo_linux_v2.0.5_extracted/BDInfo
disc=/mnt/storage/torrents/Network.1976.1080p.USA.Blu-ray.AVC.LPCM.1.0-TMT
out=/tmp/bdinfo-parity
mkdir -p "$out"

"$off" -p "$disc" -o "$out/official.txt"
/tmp/bdinfo -p "$disc" -o "$out/ours.txt"

diff -u --text "$out/official.txt" "$out/ours.txt"
```

### Oracle Test (Fuzzy Normalized)
`internal/parity/bdinfo_parity_test.go`, gated behind `BDINFO_PARITY=1`, normalizes line endings and trailing whitespace and forces both binaries to `settings.Default(...)` toggles. Defaults to the Network 1976 disc; `BDINFO_PARITY_DISC`, `BDINFO_OFFICIAL_BIN`, and `BDINFO_OFFICIAL_REPORT` (skips running the official binary) override.

```sh
BDINFO_PARITY=1 BDINFO_OFFICIAL_REPORT=/tmp/bdinfo-parity/official.txt \
go test ./internal/parity -run TestParity_OfficialBDInfo_ReportText -count=1
```

### Speed Loop
Full scans are disk-bound; compare CPU time before wall time. `scripts/speed_parity_loop.sh --disc "<disc-or-iso>" --reps 3` runs official and ours with matched toggles, checks parity per rep, and prints the median wall-time ratio. Smoke with `--reps 1` on one ISO, one static, and one Network-like disc, then `--reps 3` on the regressing sample. If Network-like discs regress, check the clip-target matching path in `internal/bdrom/streamfile.go` first.

## Fuzzing
`FUZZTIME=30s scripts/fuzz.sh` runs every fuzz target the CI job runs; add new targets to its `TARGETS` list.

## Agent skills
- Issue tracker (GitHub Issues via `gh`): `docs/agents/issue-tracker.md`
- Triage labels: `docs/agents/triage-labels.md`
- Domain docs (`CONTEXT.md`, `docs/adr/`, created lazily): `docs/agents/domain.md`
