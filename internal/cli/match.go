package cli

import "errors"

// matchUsage is --match's help text, the same on kx get and on every sweep,
// because the flag means the same thing on each: index.MatchesName.
const matchUsage = "Match by name (substring, case-insensitive)"

// errMatchBesideIndex refuses --match next to an index or a mark, in the
// voice the scope flags' refusal uses there. The reference already names one
// resource, so a term has nothing left to narrow — and silently ignoring it
// would let `kx diag 3 -m api` look as though it had checked something.
var errMatchBesideIndex = errors.New(
	"'--match' cannot be combined with an index — an index already names " +
		"one resource. Drop the flag, or drop the index to sweep the namespace instead.")

// errMCPMatchBesideTarget is errMatchBesideIndex for the MCP tools, whose
// resource argument is a target rather than an index.
//
// The term is never validated beyond that: it is compared against names kx
// already holds and never reaches an argv, so there is nothing for it to
// inject into.
var errMCPMatchBesideTarget = errors.New(
	"'match' applies without a target — a target already names one resource. " +
		"Drop it, or drop the target to sweep.")

// matchFlag renders a term for an HTML report's invocation line, so the page
// says it covers only what matched.
func matchFlag(term string) string {
	if term == "" {
		return ""
	}
	return "-m " + term
}
