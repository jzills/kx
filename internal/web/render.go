package web

import (
	"bytes"
	"embed"
	_ "embed"
	"html/template"
	"time"

	"github.com/jzills/kx/internal/render"
)

//go:embed layout.gohtml diag.gohtml scan.gohtml tree.gohtml
var templateFS embed.FS

// stylesheet is compiled in, not read at runtime — the binary ships alone.
//
//go:embed style.css
var stylesheet string

// wordmarkSVG is the same "KX" glyphs as kxArt (internal/render/help.go) and
// assets/banner.svg, kept as its own small copy rather than shared: the
// README's asset is rendered standalone by GitHub with a fixed fill and
// can't reach a Go embed directive outside this package's directory, and
// this copy carries no fill at all so it inherits the page's --accent
// through .wordmark in style.css instead.
//
//go:embed wordmark.svg
var wordmarkSVG string

// Two template sets rather than one: each body template defines "body", and a
// single set cannot hold two definitions of the same name.
var (
	diagTemplate = template.Must(template.New("diag").Funcs(funcs).
			ParseFS(templateFS, "layout.gohtml", "diag.gohtml"))
	scanTemplate = template.Must(template.New("scan").Funcs(funcs).
			ParseFS(templateFS, "layout.gohtml", "scan.gohtml"))
	treeTemplate = template.Must(template.New("tree").Funcs(funcs).
			ParseFS(templateFS, "layout.gohtml", "tree.gohtml"))
)

// RenderDiag renders a diagnostic page.
//
// Pure: no I/O, no clock, no network. Everything it needs is in the value
// passed to it, which is what lets the tests compare bytes. Ages are the one
// thing the templates would otherwise read the clock for, so this clones the
// pristine package-level template and rebinds "age" to a closure over the
// page's own Captured time before executing it — the package-level template
// itself is never executed, so later clones start from the same pristine
// funcs every time.
func RenderDiag(page DiagPage) ([]byte, error) {
	tmpl, err := diagTemplate.Clone()
	if err != nil {
		return nil, err
	}
	// Ages are rendered against the page's capture time, not the clock, so the
	// same page value always produces the same bytes.
	tmpl = tmpl.Funcs(template.FuncMap{
		"age": func(timestamp time.Time) string {
			return render.FormatAgeAt(page.Captured, timestamp)
		},
	})
	var out bytes.Buffer
	if err := tmpl.ExecuteTemplate(&out, "layout", page); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}

// RenderScan renders an image-scan page.
//
// See RenderDiag: the same clone-and-rebind keeps this pure too, even though
// today's scan.gohtml placeholder has no events yet — Task 8 fills it in, and
// the two renderers must not diverge on this.
func RenderScan(page ScanPage) ([]byte, error) {
	tmpl, err := scanTemplate.Clone()
	if err != nil {
		return nil, err
	}
	tmpl = tmpl.Funcs(template.FuncMap{
		"age": func(timestamp time.Time) string {
			return render.FormatAgeAt(page.Captured, timestamp)
		},
	})
	var out bytes.Buffer
	if err := tmpl.ExecuteTemplate(&out, "layout", page); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}

// RenderTree renders an ownership-graph page.
//
// Unlike RenderDiag/RenderScan, this needs no clone-and-rebind: a tree page
// carries no timestamps, so there is no "age" for it to bind per call, and
// executing the pristine package-level template directly is safe — it is
// never mutated after init, and html/template.Execute is safe to call
// concurrently once parsing is done.
func RenderTree(page TreePage) ([]byte, error) {
	var out bytes.Buffer
	if err := treeTemplate.ExecuteTemplate(&out, "layout", page); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}
