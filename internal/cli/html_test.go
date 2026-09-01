package cli

import "testing"

// --out already says "HTML" in its own name and description, so it implies
// --html on its own rather than being refused for lacking it. A direct test
// of the function rather than only through RunE: driven through a real
// command, a caller that stops folding --out into Enabled fails no test —
// there is no cluster to reach the html-serving code either way, so RunE
// fails for an unrelated reason before the gap is ever visible. This is the
// one place the gap is observable.
func TestImpliedHTML(t *testing.T) {
	cases := []struct {
		name string
		html bool
		out  string
		want bool
	}{
		{"neither", false, "", false},
		{"html only", true, "", true},
		{"out only", false, "report.html", true},
		{"both", true, "report.html", true},
	}
	for _, tc := range cases {
		if got := impliedHTML(tc.html, tc.out); got != tc.want {
			t.Errorf("%s: impliedHTML(%v, %q) = %v, want %v", tc.name, tc.html, tc.out, got, tc.want)
		}
	}
}

// htmlFlagName names whichever flag actually asked for the report, so a
// conflict error never blames a flag the caller didn't type.
func TestHTMLFlagName(t *testing.T) {
	if got := htmlFlagName(true); got != "--html" {
		t.Errorf("htmlFlagName(true) = %q, want --html", got)
	}
	if got := htmlFlagName(false); got != "--out" {
		t.Errorf("htmlFlagName(false) = %q, want --out", got)
	}
}

// The invariant every real call site keeps: Out set without Enabled must be
// refused loudly rather than silently writing nothing, in case a future
// caller ever forgets to derive Enabled from impliedHTML.
func TestValidateRefusesOutWithoutEnabled(t *testing.T) {
	err := htmlOptions{Out: "report.html"}.validate(false, false)
	if err == nil {
		t.Fatal("validate accepted Out set with Enabled false")
	}
}
