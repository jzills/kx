package main

import (
	"fmt"
	"strings"
	"testing"

	"github.com/jzills/kx/internal/theme"
)

func stylesheet(t *testing.T) string {
	t.Helper()
	rendered, err := render()
	if err != nil {
		t.Fatalf("render() returned an error: %v", err)
	}
	return rendered
}

// lightPalettes is the set the terminal exception below applies to. A test
// that found none would pass every assertion about them vacuously.
func lightPalettes(t *testing.T) []string {
	t.Helper()
	var light []string
	for _, name := range names() {
		isLight, err := isLight(name)
		if err != nil {
			t.Fatalf("isLight(%q) returned an error: %v", name, err)
		}
		if isLight {
			light = append(light, name)
		}
	}
	if len(light) == 0 {
		t.Fatal("no light palette in the registry: the rest of this test would assert nothing")
	}
	return light
}

// A light palette shows the default dark capture, because a capture drawn in
// light-palette colors on a white page reads as a code block rather than as a
// terminal. The rule that would override it back to the palette's own capture
// is the regression this guards.
func TestLightPaletteKeepsTheDarkCapture(t *testing.T) {
	rendered := stylesheet(t)
	for _, name := range lightPalettes(t) {
		override := fmt.Sprintf("[data-kx-theme=%q] [data-kx-terminal=%q] { display: block; }\n", name, name)
		if strings.Contains(rendered, override) {
			t.Errorf("stylesheet shows the %s palette its own terminal capture:\n%s", name, override)
		}
		hide := fmt.Sprintf("[data-kx-theme=%q] [data-kx-terminal=%q] { display: none; }\n", name, defaultDark)
		if strings.Contains(rendered, hide) {
			t.Errorf("stylesheet hides the %s capture under the %s palette:\n%s", defaultDark, name, hide)
		}
	}

	// The control for both assertions: a dark palette is still switched to
	// its own capture, so the two above cannot pass because no palette gets
	// a rule at all.
	control := fmt.Sprintf("[data-kx-theme=%q] [data-kx-terminal=%q] { display: block; }\n", "dracula", "dracula")
	if !strings.Contains(rendered, control) {
		t.Errorf("stylesheet does not switch the dracula palette to its own capture:\n%s", control)
	}
}

// The report screenshots are browser pages, not terminals, so a light palette
// keeps showing its own: the terminal exception must not spread to them.
func TestLightPaletteKeepsItsOwnReportShot(t *testing.T) {
	rendered := stylesheet(t)
	for _, name := range lightPalettes(t) {
		want := fmt.Sprintf("[data-kx-theme=%q] [data-kx-shot=%q] { display: block; }\n", name, name)
		if !strings.Contains(rendered, want) {
			t.Errorf("stylesheet does not show the %s palette its own report screenshot:\n%s", name, want)
		}
	}
}

// Under a light palette the capture shown is the dark one, so the properties
// its markup names — the body color the output inherits, the accent the
// prompt is drawn in — have to resolve to that palette's values. Left to the
// page's, the output rendered in near-black on the capture's near-black
// background.
func TestLightPaletteRecolorsTheTerminal(t *testing.T) {
	rendered := stylesheet(t)
	dark, err := theme.WebStyles(defaultDark)
	if err != nil {
		t.Fatalf("WebStyles(%q) returned an error: %v", defaultDark, err)
	}

	for _, name := range lightPalettes(t) {
		scope := fmt.Sprintf("[data-kx-theme=%q] .kx-terminal {", name)
		start := strings.Index(rendered, scope)
		if start < 0 {
			t.Fatalf("stylesheet has no %s block", scope)
		}
		block := rendered[start:]
		block = block[:strings.Index(block, "}")]

		for _, key := range []string{theme.Accent, theme.Body, theme.Surface, theme.Border} {
			want := fmt.Sprintf("  --kx-%s: %s;", strings.ReplaceAll(key, ".", "-"), dark[key])
			if !strings.Contains(block, want) {
				t.Errorf("%s block does not set %s to the %s palette's value:\n%s\nblock:\n%s",
					scope, key, defaultDark, want, block)
			}
		}

		// Hextra derives its own color ramp from these, and the ramp belongs
		// to a page: scoped to one element they would recolor the chrome of
		// whatever the capture contains.
		if strings.Contains(block, "--primary-") {
			t.Errorf("%s block carries Hextra's primary ramp:\n%s", scope, block)
		}
	}
}
