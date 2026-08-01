package web

import (
	"bytes"
	"embed"
	_ "embed"
	"html/template"
)

//go:embed layout.gohtml diag.gohtml scan.gohtml
var templateFS embed.FS

// stylesheet is compiled in, not read at runtime — the binary ships alone.
//
//go:embed style.css
var stylesheet string

// Two template sets rather than one: each body template defines "body", and a
// single set cannot hold two definitions of the same name.
var (
	diagTemplate = template.Must(template.New("diag").Funcs(funcs).
			ParseFS(templateFS, "layout.gohtml", "diag.gohtml"))
	scanTemplate = template.Must(template.New("scan").Funcs(funcs).
			ParseFS(templateFS, "layout.gohtml", "scan.gohtml"))
)

// RenderDiag renders a diagnostic page.
//
// Pure: no I/O, no clock, no network. Everything it needs is in the value
// passed to it, which is what lets the tests compare bytes.
func RenderDiag(page DiagPage) ([]byte, error) {
	var out bytes.Buffer
	if err := diagTemplate.ExecuteTemplate(&out, "layout", page); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}

// RenderScan renders an image-scan page.
func RenderScan(page ScanPage) ([]byte, error) {
	var out bytes.Buffer
	if err := scanTemplate.ExecuteTemplate(&out, "layout", page); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}
