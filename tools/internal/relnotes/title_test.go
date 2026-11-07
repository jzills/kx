package relnotes

import (
	"strings"
	"testing"
)

// A title's subject becomes a release-note sentence, so one that can't take
// a capital, or carries its own backticks or period, is refused when the PR
// is opened rather than found in the release preview — v0.12.0 shipped
// "--context reaches diag" and "kx tree is Shift-T". A prefix with a blank
// subject is one of those refusals and not a crash: "fix: " used to panic
// here, on the first word of no words, so the check exited 2 without saying
// what was wrong with the title. Blank includes the Unicode spaces the
// pattern's ASCII-only \s lets through as a subject, a no-break space among
// them, which a first fix that tightened the pattern still crashed on.
// Controls: the same subjects reworded, a
// flag later in the subject, and a word that only starts with kx, pass.
func TestTitleProblems(t *testing.T) {
	for _, tc := range []struct{ title, want string }{
		{"feat(targets): --context reaches diag", "opens with the flag '--context'"},
		{"fix(k9s): kx tree is Shift-T", "opens with 'kx'"},
		{"fix(k9s): kx's tree key is Shift-T", "opens with 'kx'"},
		{"fix(get): the `-o name` listing prints kubectl's reply", "has backticks"},
		{"fix(get): the -o name listing prints kubectl's reply.", "ends with a period"},
		{"Release v0.12.0", "isn't 'type(scope): subject'"},
		{"fix: ", "isn't 'type(scope): subject'"},
		{"fix(get):\t ", "isn't 'type(scope): subject'"},
		{"fix:  ", "isn't 'type(scope): subject'"},
		{"fix: \v", "isn't 'type(scope): subject'"},
		{"fix:  \u0085", "isn't 'type(scope): subject'"},
		{"fix(get): kubectl's path flags offer files", "opens with 'kubectl'"},
		{"feat(mcp): list_resources takes lastRun", "opens with 'list_resources'"},
		{"fix: ten findings from the fourth v0.7.0..develop review", "names a review"},
		{"fix: two regressions and two nits from #444 and #445", "names a review"},
		{"fix(cli): eight bugs from the v0.7.0..develop pass", "names a review"},
	} {
		got := TitleProblems(tc.title)
		if len(got) != 1 || !strings.Contains(got[0], tc.want) {
			t.Errorf("%q: problems = %q, want one containing %q", tc.title, got, tc.want)
		}
	}
	for _, title := range []string{
		"feat(targets): the --context flag reaches diag",
		"fix(k9s): the kx tree command is Shift-T",
		"fix(get)!: a -o name listing prints kubectl's reply",
		"chore(deps): bump k8s.io/client-go from 0.37.1 to 0.37.2",
		"docs: kxlint is mentioned",
		"fix(get): path flags from kubectl offer files",
		"feat(mcp): the list_resources tool takes lastRun",
		"docs(release): the review pass on every release's Highlights",
		"ci(review): findings are posted as comments",
	} {
		if got := TitleProblems(title); len(got) != 0 {
			t.Errorf("%q: problems = %q, want none", title, got)
		}
	}
}
