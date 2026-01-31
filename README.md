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
