package cli

import (
	"strings"
	"testing"

	"github.com/jzills/kx/internal/config"
)

// Every --since surface names the same vocabulary, and names all of it.
//
// The three commands each carried their own list — "30m, 12h, 7d" — which was
// short enough to read as exhaustive when it was only illustrative. `1s` has
// parsed since the flag existed and nothing on screen said so, so a reader
// who wanted a one-second window had no way to learn from kx that they could
// have one.
func TestSinceHelpNamesEveryDocumentedUnit(t *testing.T) {
	for _, tc := range []struct {
		command string
		usage   string
	}{
		{"diagnostic", newDiagnosticCommand(Services{}, "diagnostic", nil).
			Flags().Lookup("since").Usage},
		{"events", newEventsCommand(Services{}).Flags().Lookup("since").Usage},
		{"logs", newLogsCommand(Services{}).Flags().Lookup("since").Usage},
	} {
		if !strings.Contains(tc.usage, config.DurationUnits) {
			t.Errorf("kx %s --since help = %q, want it to name the units %q",
				tc.command, tc.usage, config.DurationUnits)
		}
		// One example per unit, so none of the four has to be inferred from
		// the other three.
		for _, window := range strings.Split(config.DurationExamples, ", ") {
			if !strings.Contains(tc.usage, window) {
				t.Errorf("kx %s --since help = %q, want %s among the examples",
					tc.command, tc.usage, window)
			}
		}
	}
}

// The seconds the help now promises have to actually parse — the list is a
// claim about the parser, not decoration.
func TestEveryDocumentedWindowParses(t *testing.T) {
	for _, window := range strings.Split(config.DurationExamples, ", ") {
		got, err := config.ParseDuration(window)
		if err != nil {
			t.Errorf("ParseDuration(%q) = %v, but the help offers it", window, err)
			continue
		}
		if got <= 0 {
			t.Errorf("ParseDuration(%q) = %v, want a real window", window, got)
		}
	}
}

// The long help promises fractions and mixtures, and promises that d takes
// the first but not the second. All four claims are the parser's to keep.
func TestTheLongHelpsFractionsAndMixturesAreReal(t *testing.T) {
	for _, value := range []string{"1.5h", "1h30m", "1.5d"} {
		if _, err := config.ParseDuration(value); err != nil {
			t.Errorf("ParseDuration(%q) = %v, but kx diag's long help offers it", value, err)
		}
	}
	if _, err := config.ParseDuration("1d12h"); err == nil {
		t.Error("ParseDuration(\"1d12h\") succeeded, but the long help says d takes no mixture")
	}
}
