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
			"The subject opens with 'kx' — open with another word, as in 'the kx tree key …'.")
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
