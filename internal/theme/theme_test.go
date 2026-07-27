package theme

import (
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
