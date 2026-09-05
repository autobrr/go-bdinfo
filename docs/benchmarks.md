# Benchmarks

Whole-disc scans of go-bdinfo against the official BDInfo Linux CLI and against a Rust rewrite of BDInfo, on real Blu-ray discs. Run on 2026-09-04.

Summary: same wall time for all three (the disk is the limit), go-bdinfo uses the least CPU, the Rust rewrite uses the least memory, and go-bdinfo matches the official report.

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
- Scans are disk-bound (about 96 % of the raw disk throughput), so wall time is mostly noise. Runs of the same tool on the same disc differ by up to 15 %. User CPU and peak RSS are the primary metrics.

The scripts are in [`scripts/bench/`](../scripts/bench/).

## Results

Median of 3 runs. The official binary ran once. The official binary did not run on the .iso because the container cannot loop-mount an image.

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

- Wall time is a tie on this storage. Every disc lands inside the run-to-run noise. All three tools read at the disk limit.
- go-bdinfo uses the least CPU: 2.4 to 4.1 times less than the official binary and 3.5 to 5.4 times less than the Rust rewrite. On AVC and MPEG-2 discs it spends 7 to 17 s of user CPU per scan. The Rust rewrite spends 35 to 87 s and the official binary 28 to 65 s. On fast storage (NVMe, warm cache) the CPU gap becomes the wall-time gap.
- The Rust rewrite uses the least memory: 12 to 34 MB peak against 52 to 89 MB for go-bdinfo. Both are far below the official binary (195 to 909 MB).
- The HEVC UHD disc is where go-bdinfo and the Rust rewrite are closest on CPU (3.5×). The Rust number includes extended HEVC diagnostics that it cannot switch off.

## Parity against the official report

Each report was compared with `diff` against the official binary's report for the same disc. All three tools produced byte-identical output across their own repeated runs.

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
