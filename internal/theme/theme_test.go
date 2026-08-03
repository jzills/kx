package theme

import (
	"regexp"
	"strings"
	"testing"
)

// Every palette must expand cleanly, or a theme that lists fine crashes the
// moment it is selected.
func TestAllRegisteredThemesExpand(t *testing.T) {
	for _, name := range Names() {
		styles, err := Styles(name)
		if err != nil {
			t.Errorf("Styles(%q): %v", name, err)
			continue
		}
		for _, key := range []string{
			Accent, Header, Muted, Body, Error, Warn, Success,
			StatusOK, StatusWarn, StatusBad, StatusNeutral,
		} {
			if styles[key] == "" {
				t.Errorf("theme %q has no %q style", name, key)
			}
		}
	}
}

// Names is the display order for `kx theme`, so it must cover the registry
// exactly — a palette missing here is invisible and unselectable in practice.
func TestNamesCoversRegistry(t *testing.T) {
	if len(Names()) != len(palettes) {
		t.Fatalf("Names() has %d entries, registry has %d", len(Names()), len(palettes))
	}
	for _, name := range Names() {
		if !Exists(name) {
			t.Errorf("Names() lists %q, which is not in the registry", name)
		}
	}
}

func TestNamesIsStable(t *testing.T) {
	first, second := Names(), Names()
	for i := range first {
		if first[i] != second[i] {
			t.Fatalf("Names() is not order-stable: %v vs %v", first, second)
		}
	}
	if first[0] != Default {
		t.Errorf("Names()[0] = %q, want the default theme %q first", first[0], Default)
	}
}

// Callers must not be able to reorder the registry through the returned slice.
func TestNamesReturnsACopy(t *testing.T) {
	names := Names()
	names[0] = "mutated"
	if Names()[0] == "mutated" {
		t.Error("Names() exposed the backing slice")
	}
}

func TestHeaderDefaultsToBoldAccent(t *testing.T) {
	styles := MustStyles("github-dark")
	if want := "bold #3fb950"; styles[Header] != want {
		t.Errorf("header = %q, want %q", styles[Header], want)
	}
}

// "mono" sets Header explicitly, so it must not be overridden by the default.
func TestExplicitHeaderIsKept(t *testing.T) {
	if got := MustStyles("mono")[Header]; got != "bold" {
		t.Errorf("mono header = %q, want %q", got, "bold")
	}
}

// The "plain" palette exists so every style resolves to the terminal default.
func TestPlainPaletteIsAllDefault(t *testing.T) {
	for key, spec := range MustStyles("plain") {
		if spec != "default" {
			t.Errorf("plain theme %q = %q, want \"default\"", key, spec)
		}
	}
}

func TestUnknownThemeIsRejected(t *testing.T) {
	_, err := Styles("nonexistent")
	if err == nil {
		t.Fatal("Styles accepted an unknown theme")
	}
	if !strings.Contains(err.Error(), "kx theme") {
		t.Errorf("error = %q, want it to name the recovery step", err)
	}
	if Exists("nonexistent") {
		t.Error("Exists reported an unknown theme as known")
	}
}

func TestStatusStylesTrackPaletteSemantics(t *testing.T) {
	styles := MustStyles("nord")
	if styles[StatusOK] != styles[Success] {
		t.Error("status.ok must track success")
	}
	if styles[StatusBad] != styles[Error] {
		t.Error("status.bad must track error")
	}
	if styles[StatusWarn] != styles[Warn] {
		t.Error("status.warn must track warn")
	}
	if styles[StatusNeutral] != styles[Body] {
		t.Error("status.neutral must track body")
	}
}

var hexColor = regexp.MustCompile(`^#[0-9a-f]{6}$`)

// webKeys is every key WebStyles must return. header is not among them: Styles
// builds it as "bold "+Accent, and boldness is a font weight in CSS, not a
// colour.
var webKeys = []string{
	Accent, Muted, Body, Error, Warn, Success,
	StatusOK, StatusWarn, StatusBad, StatusNeutral,
	Background, Surface, Border,
}

// Every registered theme must yield a literal colour for every web key.
// Without this, a palette added later ships an unstyleable page and nobody
// notices until somebody selects it.
func TestWebStylesAreHexForEveryTheme(t *testing.T) {
	for _, name := range Names() {
		styles, err := WebStyles(name)
		if err != nil {
			t.Fatalf("WebStyles(%q) returned %v", name, err)
		}
		for _, key := range webKeys {
			value, ok := styles[key]
			if !ok {
				t.Errorf("%s: missing key %q", name, key)
				continue
			}
			if !hexColor.MatchString(value) {
				t.Errorf("%s: %s = %q, want #rrggbb", name, key, value)
			}
		}
		if len(styles) != len(webKeys) {
			t.Errorf("%s: returned %d keys, want %d", name, len(styles), len(webKeys))
		}
		if _, ok := styles[Header]; ok {
			t.Errorf("%s: WebStyles must not return %q", name, Header)
		}
	}
}

// mono and plain carry terminal attributes rather than colours, so their web
// values come from the Chrome overrides instead.
func TestWebStylesUsesChromeOverrides(t *testing.T) {
	for _, name := range []string{"mono", "plain"} {
		styles, err := WebStyles(name)
		if err != nil {
			t.Fatalf("WebStyles(%q) returned %v", name, err)
		}
		if styles[Body] == "default" || styles[Muted] == "bright_black" {
			t.Errorf("%s: terminal attribute leaked into web styles: %v", name, styles)
		}
	}
}

// The status.* styles alias success/warn/error, so overriding those three has
// to carry through to all four.
func TestWebStylesAliasesStatusStyles(t *testing.T) {
	styles, err := WebStyles("github-dark")
	if err != nil {
		t.Fatalf("WebStyles returned %v", err)
	}
	if styles[StatusOK] != styles[Success] {
		t.Errorf("status.ok = %q, want success %q", styles[StatusOK], styles[Success])
	}
	if styles[StatusBad] != styles[Error] {
		t.Errorf("status.bad = %q, want error %q", styles[StatusBad], styles[Error])
	}
	if styles[StatusNeutral] != styles[Body] {
		t.Errorf("status.neutral = %q, want body %q", styles[StatusNeutral], styles[Body])
	}
}

func TestWebStylesRejectsUnknownTheme(t *testing.T) {
	if _, err := WebStyles("nope"); err == nil {
		t.Fatal("WebStyles(\"nope\") returned no error")
	}
}
