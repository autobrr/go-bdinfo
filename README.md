# go-bdinfo

Go rewrite of BDInfo.

## Installation

- Homebrew (macOS):

```sh
brew tap s0up4200/go-bdinfo https://github.com/s0up4200/go-bdinfo
brew install --cask s0up4200/go-bdinfo/bdinfo
```

- Go install (requires Go toolchain):

```sh
go install github.com/s0up4200/go-bdinfo/cmd/bdinfo@latest
```

- Latest release (one-liner, Linux x86_64):
  - Replace `linux_amd64` with `linux_arm64`, `darwin_amd64`, or `darwin_arm64` as needed.

```sh
curl -sL "$(curl -s https://api.github.com/repos/s0up4200/go-bdinfo/releases/latest | grep browser_download_url | grep linux_amd64 | cut -d\" -f4)" | tar -xz -C /usr/local/bin
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

## Options

- `-o, --reportfilename` (use `-` for stdout)
- `--stdout` (write report to stdout)
- `--main` (only main playlist; likely what you want)
- `-f, --forumsonly` (only forums paste block)
- `-s, --summaryonly` (only quick summary block; likely what you want)
- `-b, --enablessif` (default on; use `--enablessif=false` to disable)
- `-l, --filterloopingplaylists`
- `-y, --filtershortplaylist` (default on; use `--filtershortplaylist=false` to disable)
- `-v, --filtershortplaylistvalue` (seconds)
- `-k, --keepstreamorder`
- `-m, --generatetextsummary` (default on; use `--generatetextsummary=false` to disable)
- `-q, --includeversionandnotes` (default on; use `--includeversionandnotes=false` to disable)
- `-j, --groupbytime`
- `-g, --generatestreamdiagnostics`
- `-e, --extendedstreamdiagnostics` (extended HEVC video diagnostics)
- `--self-update` (update to latest release; release builds only)

## Commands

- `update` (same as `--self-update`)
- `version`
