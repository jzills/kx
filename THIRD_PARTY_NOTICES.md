# Third-party notices

kx is a Go program with no runtime dependencies (see `pyproject.toml`). The
`--html` report pages vendor one third-party JavaScript/CSS asset, compiled
into the `kx` binary via `go:embed` rather than fetched at runtime. The
documentation site under `site/` vendors one more, for the same reason.

## Tabulator

- **Version:** 6.3.1
- **Source:** https://github.com/olifolkerd/tabulator
- **License:** MIT
- **Vendored at:** `internal/web/vendor/tabulator/` (`tabulator.min.js`, `tabulator.min.css`, `LICENSE`)

The vendored files are the unmodified upstream dist build (`tabulator.min.js`
and the `tabulator_simple.min.css` base skin), fetched from jsDelivr's npm
mirror. `internal/web/kx-grid.css` layers kx's own theming on top; it is
first-party code, not part of the vendored asset.

## FlexSearch

- **Version:** 0.8.143
- **Source:** https://github.com/nextapps-de/flexsearch
- **License:** Apache-2.0
- **Vendored at:** `site/assets/vendor/flexsearch/` (`flexsearch.bundle.min.js`, `LICENSE`)

Powers the documentation site's search box. The file is the unmodified
upstream dist bundle, fetched from jsDelivr's npm mirror; the Hextra theme
would otherwise fetch the same file at build time, which would make a site
deploy depend on a CDN being up. `site/hugo.toml` points
`params.search.flexsearch.js` at the vendored copy.
