# go-bdinfo

Go rewrite of BDInfo.

## Usage

```sh
go run ./cmd/bdinfo -p /path/to/bluray
```

Report default: `BDInfo_{0}.bdinfo` (disc label substituted).

## Options

- `-p, --path` (required)
- `-o, --reportfilename`
- `--main` (only main playlist; likely what you want)
- `-f, --forumsonly` (only forums paste block)
- `-s, --summaryonly` (only quick summary block; likely what you want)
- `-b, --enablessif`
- `-l, --filterloopingplaylists`
- `-y, --filtershortplaylist`
- `-v, --filtershortplaylistvalue`
- `-k, --keepstreamorder`
- `-m, --generatetextsummary`
- `-q, --includeversionandnotes`
- `-j, --groupbytime`
- `-g, --generatestreamdiagnostics`
- `-e, --extendedstreamdiagnostics`

## Install

- Homebrew (macOS): `brew tap s0up4200/go-bdinfo` then `brew install --cask bdinfo`.
- Binaries: download the latest release from GitHub Releases.
- Build from source:
  - `go install github.com/s0up4200/go-bdinfo/cmd/bdinfo@latest`
  - or `git clone https://github.com/s0up4200/go-bdinfo.git` then `go build ./cmd/bdinfo`
