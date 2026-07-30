package render

import "testing"

// Truncation reproduces Click's make_default_short_help, which the Python CLI
// calls with a limit of 55. These expectations are the strings the Python help
// actually prints.
func TestShortHelp(t *testing.T) {
	cases := []struct{ text, want string }{
		{
			"List resources and assign index numbers for use with other commands; shorthand: kx <kind> (e.g. kx pods, kx po 3).",
			"List resources and assign index numbers for use with...",
		},
		// A first sentence that ends inside the budget is returned whole, with
		// no ellipsis.
		{
			"Show annotations for one or more indexed resources.",
			"Show annotations for one or more indexed resources.",
		},
		{
			"Set or remove labels on an indexed resource.",
			"Set or remove labels on an indexed resource.",
		},
		{
			"Navigate to the previous kx get result.",
			"Navigate to the previous kx get result.",
		},
		{"", ""},
	}
	for _, tc := range cases {
		if got := shortHelp(tc.text, shortHelpLimit); got != tc.want {
			t.Errorf("shortHelp(%q)\n got %q\nwant %q", tc.text, got, tc.want)
		}
	}
}

// Truncation is by character budget, not terminal width, so a description
// reads the same everywhere.
func TestShortHelpIsWidthIndependent(t *testing.T) {
	text := "Stream logs for an indexed resource; aggregates across pods for Deployments, StatefulSets, DaemonSets, and Services."
	first := shortHelp(text, shortHelpLimit)
	if len(first) > shortHelpLimit+3 {
		t.Errorf("shortHelp exceeded the budget: %q (%d chars)", first, len(first))
	}
	if shortHelp(text, shortHelpLimit) != first {
		t.Error("shortHelp is not deterministic")
	}
}
