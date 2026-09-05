# Benchmarks

Whole-disc scans of go-bdinfo against a BDInfo reference and a Rust rewrite of BDInfo, on real Blu-ray discs. This page contains a Linux HDD run from 2026-09-04 and a Windows NVMe run from 2026-09-06 AEST (2026-09-05 UTC). The hosts and discs differ, so cross-host comparisons describe trends rather than absolute speedups.

Summary: on the Linux HDD, go-bdinfo's median wall time ranged from 1.2% higher to 6.5% lower than Rust's because storage was the limit. On the Windows NVMe, go-bdinfo's per-disc median wall time was 11.8% to 46.1% lower than Rust's. Go used the least CPU and Rust used the least memory in both runs.

## Setup

| Item | Value |
|---|---|
| Host | LXC container, Debian 12, 12 vCPU, 4 GB RAM, kernel 7.0.14-12-pve |
| Storage | mergerfs (FUSE) over HDDs, cold sequential read 220 to 245 MB/s (`dd if=<m2ts> of=/dev/null bs=1M count=4096` after eviction) |
| go-bdinfo | commit `43c03e1`, built with `GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -trimpath -ldflags="-s -w"` |
| Rust rewrite | v4.0.0, upstream release binary for `x86_64-unknown-linux-musl`, sha256 verified against the release manifest |
| Official BDInfo | Linux CLI v2.0.5 (dotnetcorecorner), the parity reference |

## Method

- Every tool scans every playlist that the default filters keep: playlists shorter than 20 seconds and looping playlists are hidden. Stream diagnostics on, extended HEVC diagnostics off, version block off.
  - go-bdinfo: `--filterloopingplaylists=true --filtershortplaylist=true --filtershortplaylistvalue=20 --generatestreamdiagnostics=true --extendedstreamdiagnostics=false --includeversionandnotes=false`
  - Rust rewrite: `--whole --no-banner`. Its defaults apply the same filters. It has no switch for extended HEVC diagnostics, so it does more HEVC work than the other two.
  - Official: `-b true -y true -v 20 -l true -k false -g true -e false -j false -m true -q false`
- go-bdinfo and the Rust rewrite run 3 times per disc. The order alternates each rep (go, rs / rs, go / go, rs). The official binary runs once per disc as the parity reference.
- Runs are sequential. Two tools never run at the same time.
- Before every run the page cache of the disc is evicted with `posix_fadvise(POSIX_FADV_DONTNEED)` on every file of the disc. `/proc/sys/vm/drop_caches` is read-only inside the container. Eviction was verified: `Cached` in `/proc/meminfo` fell from 1.25 GB to 0.2 GB and a re-read of the same 1 GiB ran at cold disk speed again.
- Timing comes from `getrusage(RUSAGE_CHILDREN)` after the child exits: wall time, user CPU, system CPU and peak resident set size.
- Scans are disk-bound (about 96 % of the raw disk throughput), so wall time is mostly noise. The largest absolute deviation from a Go or Rust three-run median in this Linux run is 17.2 %. User CPU and peak RSS are the primary metrics.

The scripts are in [`scripts/bench/`](../scripts/bench/).

## Results

Median of 3 runs. The official binary ran once. The official binary did not run on the .iso because the container cannot loop-mount an image. Lower is better in every table.

Discs:

| Disc | Size | Content |
|---|---|---|
| Zombeavers 2014 1080p | 29 GB | AVC, DTS-HD MA, PGS |
| Bad Hombres 2023 | 19 GB | MPEG-2 1080p, DTS-HD MA |
| Britney Spears Femme Fatale Tour | 25 GB | .iso, UDF 2.50 |
| The Naked Gun 2025 MULTI | 37 GB | AVC, TrueHD, 24 subtitle streams |
| Network 1976 1080p | 45 GB | AVC, LPCM |
| Justice League Part Three 2024 UHD | 43 GB | HEVC, Dolby Vision, DTS-HD MA |

Wall time, seconds:

| Disc | go-bdinfo | Rust rewrite | official |
|---|---:|---:|---:|
| Zombeavers | 145.7 | 155.8 | 150.0 |
| Bad Hombres | 90.8 | 94.0 | 101.5 |
| Britney (.iso) | 190.0 | 197.4 | |
| The Naked Gun | 147.3 | 149.0 | 176.5 |
| Network 1080p | 217.7 | 227.9 | 240.4 |
| Justice League UHD | 169.0 | 167.1 | 166.1 |

User CPU, seconds:

| Disc | go-bdinfo | Rust rewrite | official |
|---|---:|---:|---:|
| Zombeavers | 10.5 | 54.1 | 40.6 |
| Bad Hombres | 7.0 | 34.6 | 28.0 |
| Britney (.iso) | 9.6 | 45.6 | |
| The Naked Gun | 15.4 | 82.7 | 62.5 |
| Network 1080p | 16.9 | 86.2 | 65.0 |
| Justice League UHD | 24.8 | 87.6 | 60.6 |

Peak RSS, MB:

| Disc | go-bdinfo | Rust rewrite | official |
|---|---:|---:|---:|
| Zombeavers | 52 | 13 | 284 |
| Bad Hombres | 58 | 12 | 195 |
| Britney (.iso) | 60 | 15 | |
| The Naked Gun | 61 | 24 | 909 |
| Network 1080p | 89 | 34 | 204 |
| Justice League UHD | 64 | 16 | 691 |

## Findings

- Go and Rust wall times are close on this storage: go-bdinfo's median ranges from 1.2 % higher to 6.5 % lower than Rust's. Both read at the disk limit.
- go-bdinfo uses the least CPU: the official binary uses 2.4 to 4.1 times as much user CPU, and the Rust rewrite uses 3.5 to 5.4 times as much. On AVC and MPEG-2 discs Go spends 7 to 17 s of user CPU per scan. The Rust rewrite spends 35 to 87 s and the official binary 28 to 65 s. The cold-cache Windows NVMe result below is consistent with this CPU gap becoming a wall-time gap as storage gets faster.
- The Rust rewrite uses the least memory: 12 to 34 MB peak against 52 to 89 MB for go-bdinfo. Both are far below the official binary (195 to 909 MB).
- The HEVC UHD disc is where go-bdinfo and the Rust rewrite are closest on CPU (3.5×). The Rust number includes extended HEVC diagnostics that it cannot switch off.

## Parity against the official report

Each report was compared with `diff` against the official binary's report for the same disc. Go and Rust produced byte-identical output across their own repeated runs.

### go-bdinfo

Identical to the official report, with two exceptions:

- Subtitle description. go-bdinfo fills `1920x1080 / N Captions` on every Presentation Graphics line, the same text the BDInfo GUI shows. The official Linux CLI leaves the column blank because that step only runs in the GUI. This accounts for every differing line on five of the six discs.
- Stream order on one playlist. On The Naked Gun playlist 00800 the two English PGS tracks (PIDs 0x1200 and 0x1201) are swapped. go-bdinfo and the Rust rewrite both order them by PID. The official binary's .NET sort places 0x1201 first. This is a known .NET sort-order quirk and affects 2 lines.

### Rust rewrite

The report layout follows the Windows BDInfo GUI, not the Linux CLI, so a plain `diff` touches most lines. After normalising whitespace, table rules, zero-padded times, `Mbps`/`kbps` suffixes and thousands separators, these differences remain:

- DTS core bitrate is reported as 1536 kbps where the official binary and go-bdinfo report 1509 kbps. The Rust rewrite documents this as a deliberate choice.
- Audio and subtitle streams keep playlist order, and playlists appear in a different order. The official binary and go-bdinfo sort streams (English first, then by language, then by PID).
- The forums-paste "Main Audio Track" column picks a different track on two discs (Zombeavers, Bad Hombres).
- HEVC video descriptions carry the extended fields because extended diagnostics cannot be switched off.
- Subtitle caption counts are filled, as in go-bdinfo.

## Raw data

Per run: wall s, user s, sys s, peak RSS KB, exit code.

```
zombeavers  go  149.82 10.48 37.62 53720 0 | 143.70 10.63 35.11 52928 0 | 145.70 10.23 27.38 52848 0
zombeavers  rs  182.54 54.05 37.31 13396 0 | 155.75 51.88 35.61 13236 0 | 137.41 55.16 29.52 13424 0
zombeavers  off 150.00 40.56 27.38 290784 0
badhombres  go   89.92  7.07 20.39 60004 0 |  90.77  7.01 20.28 59824 0 |  92.16  6.92 23.84 59816 0
badhombres  rs   88.33 34.55 18.18 12656 0 |  93.96 32.00 16.69 12712 0 |  98.35 36.08 23.31 12840 0
badhombres  off 101.51 28.04 15.97 199352 0
britney-iso go  210.49  9.80 23.32 58396 0 | 189.35  9.56 23.12 61924 0 | 189.95  9.63 23.22 61796 0
britney-iso rs  203.71 46.07 24.39 15776 0 | 197.35 45.64 23.61 15848 0 | 191.03 45.48 23.44 15800 0
nakedgun    go  145.14 15.29 35.26 60188 0 | 147.30 15.61 36.28 62768 0 | 156.07 15.35 35.70 68692 0
nakedgun    rs  156.89 83.81 33.34 24572 0 | 148.99 82.73 33.15 24480 0 | 146.48 82.51 32.85 24004 0
nakedgun    off 176.54 62.50 31.01 931144 0
network1080 go  224.14 16.95 43.53 94844 0 | 217.70 16.62 43.44 91516 0 | 208.26 16.89 43.93 87604 0
network1080 rs  240.74 87.46 42.45 35152 0 | 227.91 86.19 41.67 35004 0 | 226.52 82.52 39.33 35232 0
network1080 off 240.42 65.04 40.66 209032 0
jl3-uhd     go  181.96 24.81 42.26 64580 0 | 169.02 24.96 41.79 65880 0 | 167.02 24.43 41.75 69024 0
jl3-uhd     rs  168.89 87.62 40.17 16560 0 | 166.29 85.61 38.91 16400 0 | 167.06 87.64 39.11 16320 0
jl3-uhd     off 166.11 60.55 37.98 707896 0
```

## Windows NVMe benchmark

Run on 2026-09-06 AEST (2026-09-05 UTC) from five complete BDMV folders staged on NVMe. The scan scope, filters, diagnostics, process order and cold-cache intent match the Linux benchmark. The different host and disc set make the comparison with the Linux run directional.

### Setup

| Item | Value |
|---|---|
| Host | Windows 11 IoT Enterprise LTSC, build 26100; Intel Core i5-14600K, 14 cores/20 logical processors; 64 GB RAM; PowerShell 7.6.5 |
| Storage | 2 TB Crucial T705 NVMe (`CT2000T705SSD3`), NTFS; all source folders on `C:` |
| go-bdinfo | commit `c391e26`, built with Go 1.25.12 for `windows/amd64`: `CGO_ENABLED=0 go build -trimpath -ldflags="-s -w"` |
| Rust rewrite | v4.0.0, commit `7f1e321`, built locally with `cargo build --release --locked -p bdinfo-rs` |
| Windows reference | BDInfoCLI-ng v1.0.8 for Windows x86-64, source commit `237f3d7`; its report engine identifies itself as `0.7.6.4 CLI` |

### Method

- Every tool scans every playlist that the default filters keep: looping playlists and playlists shorter than 20 seconds are hidden, stream diagnostics are on, and extended HEVC diagnostics are off where configurable.
  - go-bdinfo: `--filterloopingplaylists=true --filtershortplaylist=true --filtershortplaylistvalue=20 --generatestreamdiagnostics=true --extendedstreamdiagnostics=false --includeversionandnotes=false`
  - Rust rewrite: `--whole --no-banner`. As in the Linux run, it cannot disable extended HEVC diagnostics.
  - Windows reference: `--whole`. Its equivalent scan, filter and diagnostic settings are fixed defaults. It fixes `KeepStreamOrder=true`, while the Linux reference used `-k false`, so report order is not byte-comparable.
- Go and Rust run 3 times per disc in the same alternating order as the Linux benchmark (go, rs / rs, go / go, rs). The Windows reference runs once per disc. All 35 fresh processes run sequentially with `BDINFO_WORKERS=1` in the environment.
- Before every process, Microsoft Sysinternals RAMMap 1.63 runs with `-Es` and then `-Et`. Cache clearing is outside the timed interval. The standby list was below 377 MiB after every purge.
- Wall time comes from `System.Diagnostics.Stopwatch`; user and system CPU come from `UserProcessorTime` and `PrivilegedProcessorTime`; peak working set comes from `GetProcessMemoryInfo`.
- Every run writes to a separate output directory. Exit status, command, report, selected playlists, cache state, process I/O and volume I/O are captured for validation.

Discs:

| Disc | Size | Content | Retained playlists |
|---|---:|---|---:|
| Beverly Hills Cop UHD | 66.1 GB | 2160p HEVC, HDR10/Dolby Vision, DTS-HD MA | 20 |
| Black Lightning VC-1 | 35.0 GB | 1080p VC-1, DTS-HD MA | 26 |
| Tears of the Sun MPEG-2 | 24.3 GB | 1080p MPEG-2, TrueHD | 15 |
| Scanners AVC/LPCM | 49.4 GB | 1080p AVC, LPCM | 14 |
| Live Free or Die Hard | 49.4 GB | 1080p AVC, DTS-HD MA, seamless branching | 51 |

### Results

Median of 3 runs for Go and Rust. The Windows reference ran once. Lower is better in every table.

Wall time, seconds:

| Disc | go-bdinfo | Rust rewrite | Windows reference |
|---|---:|---:|---:|
| Beverly Hills Cop UHD | 24.3 | 40.6 | 61.0 |
| Black Lightning VC-1 | 11.8 | 15.1 | 53.9 |
| Tears of the Sun MPEG-2 | 7.3 | 13.6 | 23.3 |
| Scanners AVC/LPCM | 15.4 | 18.0 | 41.5 |
| Live Free or Die Hard | 19.7 | 22.4 | 89.5 |

User CPU, seconds:

| Disc | go-bdinfo | Rust rewrite | Windows reference |
|---|---:|---:|---:|
| Beverly Hills Cop UHD | 9.1 | 29.5 | 26.8 |
| Black Lightning VC-1 | 4.2 | 13.6 | 26.4 |
| Tears of the Sun MPEG-2 | 1.5 | 12.3 | 10.1 |
| Scanners AVC/LPCM | 3.1 | 12.2 | 18.1 |
| Live Free or Die Hard | 7.0 | 17.6 | 28.6 |

Peak working set, MiB:

| Disc | go-bdinfo | Rust rewrite | Windows reference |
|---|---:|---:|---:|
| Beverly Hills Cop UHD | 123 | 37 | 1,377 |
| Black Lightning VC-1 | 80 | 27 | 663 |
| Tears of the Sun MPEG-2 | 75 | 31 | 1,150 |
| Scanners AVC/LPCM | 76 | 34 | 321 |
| Live Free or Die Hard | 111 | 55 | 2,634 |

### Findings

- On the Linux HDD, go-bdinfo's wall-time median ranged from 1.2% higher to 6.5% lower than Rust's. On the Windows NVMe, go-bdinfo's per-disc median wall time was 11.8% to 46.1% lower than Rust's. This is consistent with faster storage exposing CPU work that the HDD run hid; the different hosts and samples do not support direct absolute-speed comparisons.
- Rust used 2.5 to 8.0 times as much user CPU as go-bdinfo, while go-bdinfo used 2.0 to 3.4 times Rust's peak working set. This preserves the CPU/memory trade-off seen on Linux.
- The three-run medians limit transient slow passes. The largest was Rust's third Live Free or Die Hard run at 43.16 seconds, compared with 22.00 and 22.38 seconds; its report was byte-identical and its process-read byte count was unchanged.
- The single Windows-reference runs were slower and used more memory on these samples. They establish report behavior and are not repeated performance estimates.

### Report validation

All 35 processes exited zero. Every tool selected the same playlist multiset on every disc, and every repeated Go and Rust report was byte-stable.

The reports are not byte-identical across implementations because of version text, blank-line layout, playlist and stream ordering, and the known PGS caption-description difference. After normalising presentation and row order, go-bdinfo's file rows and stream-diagnostic counters matched the Windows reference on all five discs. Chapter rows also matched on four; Black Lightning differed only in some VC-1 average-frame-size values. Rust's stream-diagnostic counters matched on all five, but its file total-bitrate values differed on every sample, most visibly on 250 ms UHD clips.

### Raw data

Per run: wall s, user s, system s, peak working set KiB, exit code.

```text
beverly-uhd go  33.66 15.08 14.59 125904 0 | 21.99  5.94 10.69 125812 0 | 24.26  9.14 11.50 121520 0
beverly-uhd rs  40.62 29.53 16.61  37500 0 | 44.86 30.36 17.97  37316 0 | 27.71 24.06 14.67  37452 0
beverly-uhd ref 61.04 26.83  2.58 1410252 0
black-vc1   go  11.93  3.62  6.47  79136 0 | 11.78  4.22  6.61  81532 0 | 11.49  4.39  5.98  91784 0
black-vc1   rs  15.08 13.56  6.97  27848 0 | 14.52 12.45  6.73  27800 0 | 16.16 14.00  7.34  27780 0
black-vc1   ref 53.93 26.38  7.36 678956 0
tears-mpeg2 go   8.99  3.55  3.78  77096 0 |  7.12  1.44  3.44  78024 0 |  7.35  1.55  3.69  72796 0
tears-mpeg2 rs  13.63 12.34  4.66  31376 0 | 13.68 12.97  4.77  31328 0 |  9.63  8.20  4.23  31420 0
tears-mpeg2 ref 23.29 10.09  1.58 1177120 0
scanners    go  14.66  2.59  7.55  97004 0 | 15.67  3.08  8.77  75888 0 | 15.39  3.09  7.98  77704 0
scanners    rs  17.40 11.94  9.41  34844 0 | 17.99 12.20 10.38  34960 0 | 20.85 14.27 11.06  34960 0
scanners    ref 41.55 18.09  2.31 329132 0
die-hard    go  19.73  7.00  9.45 113856 0 | 16.78  5.73  9.53 113024 0 | 25.13  9.09 12.47 114036 0
die-hard    rs  22.38 17.61 13.08  55836 0 | 22.00 16.73 12.23  55888 0 | 43.16 30.66 17.80  55772 0
die-hard    ref 89.49 28.59 11.59 2697196 0
```
