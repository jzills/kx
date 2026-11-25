package main

import (
	"fmt"
	"strings"
	"testing"
)

func stylesheet(t *testing.T) string {
	t.Helper()
	rendered, err := render()
	if err != nil {
		t.Fatalf("render() returned an error: %v", err)
	}
	return rendered
}

// Every palette shows its own terminal capture and its own report
// screenshot. A palette the stylesheet has no rule pair for silently shows
// the default dark one instead: the page recolors, the capture in the middle
// of it does not.
func TestEveryPaletteSwitchesItsOwnMedia(t *testing.T) {
	rendered := stylesheet(t)

	if len(names()) < 2 {
		t.Fatal("fewer than two palettes in the registry: the loop below would assert nothing")
	}

	for _, attribute := range []string{"data-kx-terminal", "data-kx-shot"} {
		fallback := fmt.Sprintf("[%s=%q] { display: block; }\n", attribute, defaultDark)
		if !strings.Contains(rendered, fallback) {
			t.Errorf("stylesheet has no default for %s:\n%s", attribute, fallback)
		}

		for _, name := range names() {
			if name == defaultDark {
				continue
			}
			hide := fmt.Sprintf("[data-kx-theme=%q] [%s=%q] { display: none; }\n", name, attribute, defaultDark)
			show := fmt.Sprintf("[data-kx-theme=%q] [%s=%q] { display: block; }\n", name, attribute, name)
			if !strings.Contains(rendered, hide) {
				t.Errorf("the %s palette does not hide the %s %s:\n%s", name, defaultDark, attribute, hide)
			}
			if !strings.Contains(rendered, show) {
				t.Errorf("the %s palette does not show its own %s:\n%s", name, attribute, show)
			}
		}
	}
}

// The light palette is the one whose media could plausibly be left on the
// dark default — it is the only palette whose page is light, and the
// captures were briefly made an exception to exactly this rule. Named apart
// from the loop above so a regression says which palette broke.
func TestLightPaletteShowsItsOwnCapture(t *testing.T) {
	rendered := stylesheet(t)

	if defaultLight == defaultDark {
		t.Fatalf("defaultLight and defaultDark are both %q", defaultDark)
	}

	show := fmt.Sprintf("[data-kx-theme=%q] [data-kx-terminal=%q] { display: block; }\n", defaultLight, defaultLight)
	if !strings.Contains(rendered, show) {
		t.Errorf("the %s palette does not show its own capture:\n%s", defaultLight, show)
	}
	hide := fmt.Sprintf("[data-kx-theme=%q] [data-kx-terminal=%q] { display: none; }\n", defaultLight, defaultDark)
	if !strings.Contains(rendered, hide) {
		t.Errorf("the %s palette does not hide the %s capture:\n%s", defaultLight, defaultDark, hide)
	}
}

// A palette's colors reach the page as a block of custom properties. The
// rule the picker switches on is data-kx-theme, so a block keyed any other
// way leaves the palette selectable and inert.
func TestEveryPaletteHasAPropertyBlock(t *testing.T) {
	rendered := stylesheet(t)
	for _, name := range names() {
		scope := fmt.Sprintf("[data-kx-theme=%q] {\n", name)
		start := strings.Index(rendered, scope)
		if start < 0 {
			t.Errorf("stylesheet has no %s block", scope)
			continue
		}
		block := rendered[start+len(scope):]
		end := strings.Index(block, "}")
		if end < 0 {
			t.Errorf("the %s block is never closed", name)
			continue
		}
		block = block[:end]

		// Hextra derives its own ramp from the accent, so a block without
		// these recolors the page's own components and not the theme's.
		for _, property := range []string{"--kx-accent:", "--kx-background:", "--kx-body:", "--primary-hue:"} {
			if !strings.Contains(block, property) {
				t.Errorf("the %s block does not set %s:\n%s", name, property, block)
			}
		}
	}
}
