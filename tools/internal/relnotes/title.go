package relnotes

import (
	"fmt"
	"regexp"
	"strings"
)

// TitleProblems is what keeps a pull request title from becoming release
// copy: each one a sentence naming the fix, none when it is fine. The title's
// subject becomes a bullet, capitalized, with its flags put in backticks
// (sentence), so it has to open with a word that can take a capital, and
// carry no backticks of its own. A subject opening with a flag or "kx" was
// left lowercase, or opened with code, in v0.12.0's notes:
// "--context reaches diag" and "kx tree is Shift-T".
func TitleProblems(title string) []string {
	match := conventional.FindStringSubmatch(title)
	if match == nil {
		return []string{"The title isn't 'type(scope): subject' — see STYLE.md's Commits and pull requests."}
	}
	subject := match[4]
	var problems []string
	first := strings.Fields(subject + " ")[0]
	switch {
	case strings.HasPrefix(first, "-"):
		problems = append(problems, fmt.Sprintf(
			"The subject opens with the flag '%s' — open with a word, as in 'the %s flag …'.", first, first))
	case titleOpensWithKx.MatchString(subject):
		problems = append(problems,
			"The subject opens with 'kx' — open with another word, as in 'the kx tree command …'.")
	case titleOpensWithKubectl.MatchString(subject):
		problems = append(problems,
			"The subject opens with 'kubectl' — open with another word, as in 'the kubectl flags …'.")
	case titleOpensWithIdentifier.MatchString(subject):
		problems = append(problems, fmt.Sprintf(
			"The subject opens with '%s', which can't take a capital — open with a word, as in 'the %s tool …'.",
			titleOpensWithIdentifier.FindString(subject), titleOpensWithIdentifier.FindString(subject)))
	}
	// Only a type that becomes a bullet: a docs or ci PR about a review is
	// about a review, and no reader of the notes sees its title.
	if bulletType[match[1]] && reviewBatch.MatchString(subject) {
		problems = append(problems,
			"The subject names a review, not a change — say what a user would notice, or split the PR by what it changes.")
	}
	if strings.Contains(subject, "`") {
		problems = append(problems, "The subject has backticks — leave them out; the release notes add them to flags.")
	}
	if strings.HasSuffix(subject, ".") {
		problems = append(problems, "The subject ends with a period — leave it off; the release notes add one.")
	}
	return problems
}

// titleOpensWithKx is a subject whose first word is kx, possessive included.
var titleOpensWithKx = regexp.MustCompile(`^kx($|[\s'’,.:;])`)

// titleOpensWithKubectl and titleOpensWithIdentifier are the other subjects
// that can't take the capital the release notes give a subject: "kubectl"
// and a lowercase identifier such as list_resources. v0.9.0 and v0.11.0
// shipped "Kubectl's path flags offer files" and "List_resources, top and
// the diagnose sweep take a sort".
var (
	titleOpensWithKubectl    = regexp.MustCompile(`^kubectl($|[\s'’,.:;])`)
	titleOpensWithIdentifier = regexp.MustCompile(`^[a-z0-9]+_[a-z0-9_]+\b`)
)

// reviewBatch is a subject that names a review rather than a change: "Ten
// findings from the fourth v0.7.0..develop review", "two regressions and two
// nits from the review of #444 and #445". Each became a release-note bullet
// that told a user nothing. It is a pattern, not a judgment of meaning, so it
// catches the shapes those titles took.
var reviewBatch = regexp.MustCompile(`(?i)\b(findings|nits)\b|\breview\b|\.\.develop\b`)

// bulletType is a type whose title becomes a release-note bullet.
var bulletType = map[string]bool{"feat": true, "fix": true, "perf": true}
