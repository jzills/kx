package web

import _ "embed"

// Tabulator (MIT) is vendored as a single-file browser build rather than
// fetched or built at deploy time — see vendor/tabulator/LICENSE and
// THIRD_PARTY_NOTICES.md. This keeps the "no other toolchain is required"
// promise in CLAUDE.md true: no npm, no bundler, just another asset compiled
// into the binary the same way style.css already is.

//go:embed vendor/tabulator/tabulator.min.js
var tabulatorJS string

//go:embed vendor/tabulator/tabulator.min.css
var tabulatorCSS string
