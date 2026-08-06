# Third-party notices

kx is a Go program with no runtime dependencies (see `pyproject.toml`). The
`--html` report pages vendor one third-party JavaScript/CSS asset, compiled
into the `kx` binary via `go:embed` rather than fetched at runtime.

## Tabulator

- **Version:** 6.3.1
- **Source:** https://github.com/olifolkerd/tabulator
- **License:** MIT
- **Vendored at:** `internal/web/vendor/tabulator/` (`tabulator.min.js`, `tabulator.min.css`, `LICENSE`)

The vendored files are the unmodified upstream dist build (`tabulator.min.js`
and the `tabulator_simple.min.css` base skin), fetched from jsDelivr's npm
mirror. `internal/web/kx-grid.css` layers kx's own theming on top; it is
first-party code, not part of the vendored asset.
