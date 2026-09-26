# Contributing to kx

## Development

Go, at the version pinned by the `go` directive in `go.mod`. Nothing else is
required to build or run.

```bash
go build ./...
go run ./cmd/kx --help              # run the CLI directly
gofmt -l ./cmd ./internal ./tools   # must print nothing
go vet ./...
go test -race ./...
```

`pre-commit run --all-files` runs gofmt and go vet, and regenerates the
README's command table from the command tree — it fails if the table has
drifted from the commands it documents. Tests are not in the hook — run them
yourself.

## Demos

The demo GIFs are rendered from [VHS](https://github.com/charmbracelet/vhs)
tapes — see [`demo/README.md`](demo/README.md) for seeding the demo namespace
and re-recording.

## Releases

Releases are cut by pushing a `release/vX.Y.Z` branch — see
[`RELEASING.md`](RELEASING.md).
