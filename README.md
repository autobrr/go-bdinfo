# go-bdinfo

Go rewrite of BDInfo.

## Install

- Homebrew (macOS): `brew tap s0up4200/go-bdinfo` then `brew install --cask bdinfo`.
- Go install: `go install github.com/s0up4200/go-bdinfo/cmd/bdinfo@latest`
- Latest release (one-liner, Linux x86_64):
  - Replace `linux_amd64` with `linux_arm64`, `darwin_amd64`, or `darwin_arm64` as needed.

```sh
curl -sL "$(curl -s https://api.github.com/repos/s0up4200/go-bdinfo/releases/latest | grep browser_download_url | grep linux_amd64 | cut -d\" -f4)" | tar -xz -C /usr/local/bin
```

## Usage

```sh
bdinfo --main -p /path/to/bluray
bdinfo --forumsonly --main -p /path/to/bluray
bdinfo --summaryonly -p /path/to/bluray
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
- `--self-update` (update to latest release)
