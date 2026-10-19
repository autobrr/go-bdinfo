```
██████╗ ██████╗ ██╗███╗   ██╗███████╗ ██████╗
██╔══██╗██╔══██╗██║████╗  ██║██╔════╝██╔═══██╗
██████╔╝██║  ██║██║██╔██╗ ██║█████╗  ██║   ██║
██╔══██╗██║  ██║██║██║╚██╗██║██╔══╝  ██║   ██║
██████╔╝██████╔╝██║██║ ╚████║██║     ╚██████╔╝
╚═════╝ ╚═════╝ ╚═╝╚═╝  ╚═══╝╚═╝      ╚═════╝
```

Go rewrite of BDInfo.

## Installation

- Homebrew (macOS):

```sh
brew tap autobrr/go-bdinfo https://github.com/autobrr/go-bdinfo
brew install --cask autobrr/go-bdinfo/bdinfo
```

- Go install (requires Go toolchain):

```sh
go install github.com/autobrr/go-bdinfo/cmd/bdinfo@latest
```

- Latest release (one-liner, Linux x86_64):
  - Replace `linux_amd64` with `linux_arm64`, `darwin_amd64`, or `darwin_arm64` as needed.

```sh
curl -sL "$(curl -s https://api.github.com/repos/autobrr/go-bdinfo/releases/latest | grep browser_download_url | grep linux_amd64 | cut -d\" -f4)" | tar -xz -C /usr/local/bin
```

## Usage

Recommended (likely what you want):

```sh
bdinfo /path/to/bluray --summaryonly --main
```

```sh
bdinfo /path/to/bluray --summaryonly --main --stdout
bdinfo /path/to/bluray --forumsonly --main
bdinfo /path/to/bluray --main
bdinfo /path/to/bluray --summaryonly
bdinfo update
bdinfo version
```

Path is required (ISO file or Blu-ray folder).

Report default: `BDInfo_{0}.bdinfo` (disc label substituted).

## Performance

A full-disc scan runs at the speed of the disk. go-bdinfo matches the official BDInfo 2.0.5 CLI on wall time and report content, with 2.4 to 4.1 times less CPU and 2 to 15 times less memory. See [docs/benchmarks.md](docs/benchmarks.md) for discs, method, raw numbers and the parity diff.

## Library Usage

Use the exported API package instead of importing `internal/*`:

```go
package main

import (
  "context"
  "fmt"
  "os"

  "github.com/autobrr/go-bdinfo/pkg/bdinfo"
)

func main() {
  settings := bdinfo.DefaultSettings(".")
  result, err := bdinfo.Run(context.Background(), bdinfo.Options{
    Path:     "/path/to/disc/or.iso",
    Settings: settings,
  })
  if err != nil {
    panic(err)
  }
  if result.ReportPath == "-" {
    fmt.Print(result.Report)
    return
  }
  _ = os.WriteFile(result.ReportPath, []byte(result.Report), 0o644)
}
```

Notes:
- `Run` processes a single disc path per call.
- The API returns structured metadata (`Result.Disc`, `Result.Playlists`, `Result.Scan`) and rendered report content (`Result.Report`).
- `Result.QuickSummary` and `Result.ForumsBlock` hold the `--summaryonly` and `--forumsonly` text from the same scan. They are always filled and do not depend on `SummaryOnly` or `ForumsOnly`.
- File writing is caller-owned.

### Optional video collection and exact timelines

Set `Options.IncludeTimeline` to populate `Result.Timelines`. These retain the
original unsigned MPLS IN/OUT times and integer playlist offsets in **45 kHz ticks**.
Each item keeps its clip, repeated uses, alternate angles, video mappings, join
condition, and CLPI clock sequence. `PlayItem.Angles[0]` is the default angle;
other angles are available for inspection. Configurable angle selection is not
implemented. The development-only `SelectedAngle` and `SelectedAlternative`
fields have been removed; callers should check that `Angles` is nonempty and use
its first entry for the default path.

`Run` returns timelines for the playlists contributing to its selected report.
Summary and full-report selection can differ. `DiscoverPlaylists` returns timing
metadata for every discovered playlist, or the explicit `Settings.PlaylistOnly`
selection. Report filters do not narrow discovery. Discovery never calls a collector.

Set `Options.VideoConsumer` to a `video.Factory` from
`github.com/autobrr/go-bdinfo/pkg/bdinfo/video` to collect complete video elementary
streams during the existing scan. This also enables timeline capture. Return
`(nil, nil)` to decline a source/PID. Inspect the per-occurrence mappings to select
primary HEVC. PID order and bitrate do not identify the primary stream. A physical
source/PID is collected once, even when several playlists or occurrences use it.
Collection runs before main/biggest report selection. `Settings.PlaylistOnly`
restricts scanning to that playlist's sources. Otherwise collection can cover the
broader disc scan. Decline unneeded sources/PIDs in the factory to limit decoder work.

Each accepted consumer receives ordered synchronous `Consume` and
`Discontinuity` calls and exactly one `Finish`. Different physical sources may
run concurrently. Each consumer needs independent parser state; synchronize any
shared result maps. Data and PES-boundary slices are borrowed until `Consume`
returns. Callbacks provide backpressure and should observe cancellation. BDInfo
cannot interrupt a blocked callback. `Finish` has no context and must release
resources promptly even after cancellation.

Batches contain ES payload beyond the report's 5 MiB probe, excluding TS/PES
headers and bounded-PES padding. Their boundaries retain absolute ES offsets,
physical source packet positions, and original **90 kHz PTS/DTS modulo 2^33**.
Zero is a valid timestamp. Batches and PES starts are not frame boundaries.
Discontinuities establish new segments; damaged input remains incomplete even
after resynchronization. Only `End.CleanEOF` permits normal decoder finalization.

Check every required `Result.Collection` outcome and `PlaylistTimeline.Complete`
before assembling playlist metadata. Optional collector errors preserve valid
reports. The wrapped errors remain available in collection results. A complete
timeline confirms timing and clock associations; it does not certify HDR10+ frames.

Missing or contradictory STC clock information produces an explicit error. So do
unverified SSIF-to-M2TS packet mappings. STC packet ranges must fit the logical
M2TS file. Discovery assumes 192-byte M2TS framing. A scan that finds bare 188-byte
TS invalidates that clock association but still collects the elementary stream.
CLPI currently requires one complete program sequence starting at packet zero.
Later or multiple programs need packet-aware mapping. Duplicate PIDs and ambiguous
program tables cannot establish complete associations or collection.

The source path identifies the actual container relative to the disc root.
Persistent caches also need a caller-owned source fingerprint and parser version.

The compiling [`ExampleOptions_VideoConsumer`](pkg/bdinfo/example_collection_test.go)
shows a constant-memory primary-HEVC byte counter and exact occurrence intervals.
For complete HDR10+ extraction, see the separate
[go-hdr10-plus development bridge](https://github.com/Audionut/go-hdr10-plus/tree/d82b42542ac94460fbfa790154364d5ff823f575/integration/bdinfo).
Its [compiling example](https://github.com/Audionut/go-hdr10-plus/blob/d82b42542ac94460fbfa790154364d5ff823f575/integration/bdinfo/example_test.go)
collects typed metadata and handles failed playlist extraction explicitly.
The decoder owns frame association, inherited metadata, presentation order, and
playlist assembly. Its join tests use both the connection condition and decoded
HEVC state; the connection condition alone cannot prove a seamless join.
The linked bridge revision predates removal of the angle-selection fields and
needs that migration before it can build against this branch. The bridge is a
separate development module with local replacements and its own Go version
requirement. It is not a released dependency of BDInfo.

The same transport callbacks can feed Dolby Vision metadata extraction. They do
not identify or pair separate base-layer and enhancement-layer tracks. A complete
transport scan is not proof of complete Dolby Vision metadata extraction; the
external decoder must validate the input layout and frame associations.

Use keyed `Options` and `Result` literals. Adding fields preserves keyed callers,
but Go requires positional struct literals to specify every field.
Nil collection and disabled timeline capture retain the normal scan without
collection buffers, detailed timeline allocations, or new workers.

## Options

- `-o, --reportfilename` (use `-` for stdout)
- `--stdout` (write report to stdout)
- `--main` (only main playlist; likely what you want)
- `-f, --forumsonly` (only forums paste block)
- `-s, --summaryonly` (only quick summary block; likely what you want)
- `-b, --enablessif` (default on; use `--enablessif=false` to disable)
- `-l, --filterloopingplaylists` (default on; use `--filterloopingplaylists=false` to disable)
- `-y, --filtershortplaylist` (default on; use `--filtershortplaylist=false` to disable)
- `-v, --filtershortplaylistvalue` (seconds)
- `-k, --keepstreamorder`
- `-m, --generatetextsummary` (default on; use `--generatetextsummary=false` to disable)
- `-q, --includeversionandnotes` (default on; use `--includeversionandnotes=false` to disable)
- `-j, --groupbytime`
- `-g, --generatestreamdiagnostics`
- `-e, --extendedstreamdiagnostics` (extended HEVC video diagnostics)
- `--progress` (print scan progress to stderr)
- `--self-update` (update to latest release; release builds only)
- `BDINFO_WORKERS` env var overrides scan worker count (default: 2)

## Commands

- `update` (same as `--self-update`)
- `version`

## License

GPL-2.0-or-later. See [LICENSE](LICENSE).

go-bdinfo is a Go port of [BDInfo](https://github.com/UniqProject/BDInfo), which is licensed under LGPL-2.1. Section 3 of that license permits this port to be distributed under the GPL. See [NOTICE](NOTICE) for upstream attribution.
