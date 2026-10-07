package cli

import (
	"errors"
	"fmt"
	"strings"
	"unicode"

	"github.com/jzills/kx/internal/index"
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

// errMatchBesideContextIndex is errMatchBesideIndex for kx get contexts,
// whose index names the context to switch to rather than a resource in a
// namespace to sweep.
var errMatchBesideContextIndex = errors.New(
	"'--match' cannot be combined with an index — the index already names the " +
		"context to switch to. Drop the flag, or drop the index to list the contexts it matches.")

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

// A --match term narrows what kx prints, or the command is refused: a term
// that narrowed nothing and said nothing looks as though it had checked
// something, which is what errMatchBesideIndex refuses beside an index. It
// used to be applied only where kx read kubectl's reply as a table, and every
// other shape passed through untouched — kx get pods -m web -o name | xargs
// kubectl delete deleted every pod in the namespace.
//
// What kx can narrow depends on the format kubectl was asked for
// (replyFormatOf). Where the arguments alone decide it cannot, the command is
// refused before kubectl is asked for anything (matchFormatError); a table
// kx turns out unable to read is refused from the reply (narrowText).

// replyFormat is how kx narrows kubectl's reply in the format it was asked
// for.
type replyFormat int

const (
	// tableFormat is a table — kubectl's own, wide or custom columns —
	// narrowed by its rows' names, read from its NAME column.
	tableFormat replyFormat = iota
	// namesFormat is -o name: one kind/name a line, narrowed line by line.
	namesFormat
	// documentFormat is anything else — JSON, YAML, a template's text —
	// which has no rows a term could pick.
	documentFormat
)

// replyFormatOf reads the format kubectl's reply comes back in off args.
func replyFormatOf(args []string) replyFormat {
	if printsTable(args) {
		return tableFormat
	}
	if outputFormat(args) == "name" {
		return namesFormat
	}
	return documentFormat
}

// outputFormat is the -o format args ask for, without its argument:
// "jsonpath" for -o jsonpath={...}.
func outputFormat(args []string) string {
	output, _, _ := extractString(args, "--output", "-o")
	format, _, _ := strings.Cut(output, "=")
	return format
}

// matchFormatError refuses a --match term beside output the arguments alone
// say kx cannot narrow: a document, or a watch kx streams as kubectl sends it
// rather than drawing it itself (wantsLiveTable). nil when kx can narrow it.
func matchFormatError(args []string) error {
	if replyFormatOf(args) == documentFormat {
		return fmt.Errorf("'--match' cannot be combined with '-o %s' — kx narrows a table or "+
			"-o name by each row's name, and that output has no rows for it to pick. "+
			"Drop the flag, or select with -l instead.", outputFormat(args))
	}
	if isWatch(args) && !wantsLiveTable(args) {
		return fmt.Errorf("'--match' cannot be combined with '--watch -o %s' — kx narrows the "+
			"live table it draws, and streams that format as kubectl sends it. "+
			"Drop the flag, or the -o.", outputFormat(args))
	}
	return nil
}

// narrowText narrows by term a reply kx could not read as a table: -o name
// line by line, and anything else refused. An empty reply has nothing to
// narrow, and neither does an empty term.
func narrowText(output, term string, args []string) (string, error) {
	if term == "" || strings.TrimSpace(output) == "" {
		return output, nil
	}
	switch replyFormatOf(args) {
	case namesFormat:
		return narrowNames(output, term), nil
	case tableFormat:
		// A table format kx could not read: no NAME column to find names
		// in, or no header to find the column by.
		if noHeaders, _ := extractBool(args, "--no-headers"); noHeaders {
			return "", errors.New("'--match' cannot be combined with '--no-headers' — kx finds each " +
				"row's name under kubectl's NAME header. Drop one of them.")
		}
		return "", errors.New("'--match' narrows by the NAME column, and kubectl's reply has none — " +
			"add one to the custom columns (NAME:.metadata.name), or drop the flag.")
	}
	return "", matchFormatError(args)
}

// narrowNames keeps the lines of -o name output whose name contains term, as
// index.FilterRows keeps a table's rows: the name, not the kind/ kubectl puts
// in front of it, since "app" is in every "deployment.apps/…".
func narrowNames(output, term string) string {
	matches := index.NameMatcher(term)
	var kept []string
	for _, line := range strings.Split(output, "\n") {
		name := strings.TrimSpace(line)
		if slash := strings.LastIndex(name, "/"); slash >= 0 {
			name = name[slash+1:]
		}
		if name != "" && matches(name) {
			kept = append(kept, line)
		}
	}
	return strings.Join(kept, "\n")
}
