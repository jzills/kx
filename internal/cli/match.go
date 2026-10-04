package cli

import (
	"errors"
	"strings"
	"unicode"
)

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
var errMCPMatchBesideTarget = errors.New(
	"'match' applies without a target — a target already names one resource. " +
		"Drop it, or drop the target to sweep.")

// validMatch refuses an MCP match term holding a control character.
//
// The term is compared against names kx already holds and never reaches an
// argv, but under --write-listings it is saved with the listing, and kx
// prints a saved term back to the user's terminal: kx state's caption for an
// emptied listing, the refusal of an index into one, the command a failed
// refresh names. An escape sequence in an agent's term was written to that
// terminal as it stood. No resource name holds a control character, so the
// refusal costs no term that could match; anything a name can hold — RBAC's
// "system:controller:…" included — still passes.
//
// The error leaves the term out, since echoing it is what is being refused.
func validMatch(term string) error {
	if strings.IndexFunc(term, unicode.IsControl) >= 0 {
		return errors.New("'match' cannot hold a control character — no resource name does.")
	}
	return nil
}

// matchOf is a term as a saved query records it: nil for none, so a listing
// that was not narrowed reads as one rather than as one narrowed by "".
func matchOf(term string) *string {
	if term == "" {
		return nil
	}
	return &term
}

// matchFlag renders a term for an HTML report's invocation line, so the page
// says it covers only what matched.
func matchFlag(term string) string {
	if term == "" {
		return ""
	}
	return "-m " + term
}
