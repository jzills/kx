// Stale-state detection and recovery.
//
// When a command fails because its indexed resource no longer exists (pod
// churn), the command that produced the current state entry — kx get, kx top,
// or a kx diag or kx tree — is run again, saving the fresh listing so the user
// can pick a new index. The original command is never retried — the
// index→name mapping may have shifted, so retrying could act on a different
// resource than the one that was asked for.
package cli

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/jzills/kx/internal/graph"
	"github.com/jzills/kx/internal/kinds"
	"github.com/jzills/kx/internal/kubectl"
	"github.com/jzills/kx/internal/render"
	"github.com/jzills/kx/internal/state"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
)

// StaleResourceError reports that a probe confirmed the indexed resource no
// longer exists.
type StaleResourceError struct {
	Kind kinds.Kind
	Name string
	// Namespace is rendered as "in <namespace>" for a mark failure. Empty for
	// a cluster-scoped kind (Node), which genuinely has none — the message
	// must not read "in " with nothing after it.
	Namespace string
	// Ref is what named the resource. A mark failure is not refreshable: a
	// mark carries no query, and replaying the history stack's would answer
	// it with an unrelated listing.
	Ref state.Ref
}

func (e StaleResourceError) Error() string {
	if e.Ref.Mark != "" {
		where := ""
		if e.Namespace != "" {
			where = " in " + e.Namespace
		}
		return fmt.Sprintf(
			"%s is %s/%s%s, which no longer exists. Re-mark it with 'kx mark %s <index>'.",
			e.Ref, e.Kind, e.Name, where, e.Ref.Mark)
	}
	return string(e.Kind) + "/" + e.Name + " no longer exists"
}

// kubectl reports a missing resource in these shapes; there is no exit code
// that distinguishes it, so the message is all there is to go on.
var notFoundMarkers = []string{"(NotFound)", "not found"}

// IsNotFound reports whether kubectl said a resource is missing.
//
// The message is matched only after the error is known to be kubectl's. That
// order is the whole point: "not found" is not a rare phrase, and the marker
// list cannot be made specific enough to be safe on an arbitrary error —
// scanner.NotFoundError reads "grype not found on PATH", which is the same
// words about something that was never a resource. Two releases patched that
// collision by excluding a type at the call sites it reached; the collision was
// never in kubectl's wording, but in the pattern being applied to errors
// kubectl never produced, and requiring the type here ends it everywhere at
// once rather than one call site at a time.
func IsNotFound(err error) bool {
	var reported kubectl.Error
	if !errors.As(err, &reported) {
		return false
	}
	for _, marker := range notFoundMarkers {
		if strings.Contains(reported.Stderr, marker) {
			return true
		}
	}
	return false
}

// ensureExists converts a command failure into a StaleResourceError when the
// resource is genuinely gone, so the caller can refresh rather than report a
// confusing kubectl message. ref is carried onto the error unchanged, so a
// mark failure reports rather than replays — see isStale.
func ensureExists(kubectl kubectl.Service, kind kinds.Kind, name, namespace string, ref state.Ref) error {
	if kubectl.Probe([]string{"get", string(kind), name, "-n", namespace}) != 0 {
		return StaleResourceError{Kind: kind, Name: name, Namespace: namespace, Ref: ref}
	}
	return nil
}

// staleIfMissing is ensureExists for a command that reads through client-go:
// its not-found error is the API server's own, typed, rather than kubectl's
// stderr, which IsNotFound deliberately will not read. kx tree and kx diag
// printed `pods "x" not found` for an index whose resource had gone and
// stopped there, where every kubectl-backed command refreshed the listing.
//
// Only a not-found naming the indexed resource itself is stale: something
// else the command read being missing says nothing about the listing, and is
// returned as it came.
func staleIfMissing(err error, kind kinds.Kind, name, namespace string, ref state.Ref) error {
	var status apierrors.APIStatus
	if !apierrors.IsNotFound(err) || !errors.As(err, &status) {
		return err
	}
	if details := status.Status().Details; details == nil || details.Name != name {
		return err
	}
	return StaleResourceError{Kind: kind, Name: name, Namespace: namespace, Ref: ref}
}

// forwardExit turns a non-zero kubectl exit into the error kx should return.
// ref is passed straight through to ensureExists, so the caller's mark or
// index rides along onto whichever error comes back.
//
// A vanished resource becomes StaleResourceError, so the caller refreshes.
// Anything else forwards kubectl's own exit code: kubectl has already printed
// its message, so there is nothing to add, but exiting 0 would tell a script
// the command succeeded. Returning nil here — which is what ensureExists does
// on its own when the resource is still there — is why `kx describe 1
// --bogus-flag` printed kubectl's error and then exited 0.
func forwardExit(
	kubectl kubectl.Service, kind kinds.Kind, name, namespace string, code int, ref state.Ref,
) error {
	if err := ensureExists(kubectl, kind, name, namespace, ref); err != nil {
		return err
	}
	return SilentError{Code: code}
}

// isStale reports whether an error means the current state entry no longer
// describes the cluster the user is talking to.
//
// A context mismatch qualifies for the same recovery even though the listing
// itself is intact: it describes a different cluster, so the indexes in it are
// no more usable here than vanished ones, and replaying the query is what puts
// usable indexes back on screen. Crucially, recovery relists without ever
// retrying the original command — which is the whole reason a mismatch can be
// routed here rather than merely reported.
func isStale(err error) bool {
	var stale StaleResourceError
	if errors.As(err, &stale) {
		// A mark carries no query to replay — see StaleResourceError.Ref — so
		// only an index failure is refreshable here.
		return stale.Ref.Mark == ""
	}
	var mismatch state.ContextMismatchError
	if errors.As(err, &mismatch) {
		// Only when kx can rebuild what the index counted against. A slot sets
		// Relist because it has no query of its own, and replaying the history
		// stack's query for it would answer `kx ns 2` with a pods table — a
		// listing that is fresh, correct, and about something else. Those errors
		// carry their own relist hint and are reported as they are.
		return mismatch.Relist == ""
	}
	// A missing scanner used to need excluding here by type: IsNotFound read
	// the bare substring "not found" off any error, and scanner.NotFoundError
	// reads "grype not found on PATH — install it to run this scan.", so a
	// scanner that vanished between kx scan's preflight and the scan printed
	// its install message and then "Run 'kx get <resource>' to refresh the
	// list." — relisting a listing that was never the problem.
	//
	// The exclusion is gone because it has nothing left to do: IsNotFound now
	// requires a kubectl.Error, and a scanner's error is not one. Every other
	// error kx constructs is covered by the same change, rather than each
	// being discovered and excluded in turn.
	return IsNotFound(err)
}

// refreshLead introduces the relisted table, naming the reason it was relisted.
// "State was stale" describes the wrong problem for a mismatch: nothing about
// the listing has decayed, it simply belongs to another cluster.
func refreshLead(err error) string {
	var mismatch state.ContextMismatchError
	if errors.As(err, &mismatch) {
		return "Listing was from context '" + mismatch.Listed +
			"' — refreshed against '" + mismatch.Current + "', pick a new index:"
	}
	return "State was stale — refreshed, pick a new index:"
}

// recoverOutcome is what a refresh attempt produced.
type recoverOutcome int

const (
	// refreshed: the listing was re-run and rendered.
	refreshed recoverOutcome = iota
	// noQuery: the entry records no command to run again — it was saved
	// before its command recorded one — so the way forward is a listing the
	// user runs.
	noQuery
	// replayFailed: the replay broke on its own terms — most often because the
	// saved query names the very resource that went stale, which is what a
	// relist's query does. Its reason does not reach the screen: Run captures
	// stdout and returns stderr as the error, and that error is dropped here.
	// So this is treated like noQuery, and the caller ends with the same
	// instruction rather than with silence.
	replayFailed
)

// recoverState runs the command behind the current state entry again and
// renders the fresh listing under lead, which names why it was re-run. The
// query comes back too, for the instruction a failed replay ends with.
//
// Each command lists as it would typed: kx top's usage table, kx diag's
// triage table, kx tree's walk. A refresh only ever prints — the flags that
// send a listing to a browser or a script were never recorded.
func recoverState(ctx context.Context, services Services, lead string) (recoverOutcome, *state.Query) {
	current, err := services.State.Load()
	if err != nil {
		return replayFailed, nil
	}
	query := current.Query
	if query == nil {
		return noQuery, nil
	}
	var replay func() (func(), error)
	switch query.Command {
	case "":
		replay = func() (func(), error) { return replayGet(services, *query) }
	case state.CommandTop:
		replay = func() (func(), error) { return replayTop(services, *query) }
	case state.CommandDiag:
		replay = func() (func(), error) { return replaySweep(ctx, services, *query) }
	case state.CommandTree:
		replay = func() (func(), error) { return replayTree(ctx, services, *query) }
	case state.CommandFetch:
		// Nothing runs it again, but it names the listing to run instead.
		return noQuery, query
	default:
		return noQuery, nil
	}
	show, err := replay()
	if err != nil {
		return replayFailed, query
	}
	render.Raw(lead)
	show()
	return refreshed, query
}

// Each replay lists and saves, then returns what draws the listing, so
// nothing is drawn — not even the lead — for a replay that failed.

func replayGet(services Services, query state.Query) (func(), error) {
	get := GetCommand{Kubectl: services.Kubectl, State: services.State, Index: services.Index}
	table, namespace, err := get.Execute(query.Resource, queryMatch(query), query.Args)
	if err != nil {
		return nil, err
	}
	return func() { render.IndexedTable(table, query.Subject(), namespace) }, nil
}

func replayTop(services Services, query state.Query) (func(), error) {
	noLimits, rest := extractBool(query.Args, "--no-limits")
	table, label, namespace, _, err := topListing(
		services, query.Resource == "nodes", queryMatch(query), rest, noLimits)
	if err != nil {
		return nil, err
	}
	return func() { render.IndexedTable(table, label, namespace) }, nil
}

func replaySweep(ctx context.Context, services Services, query state.Query) (func(), error) {
	namespace, all, rest := recordedScope(query.Args)
	since, _, err := extractString(rest, "--since", "")
	if err != nil {
		return nil, err
	}
	window, err := resolveWindow(since, services.Config.DiagMaxAge)
	if err != nil {
		return nil, err
	}
	result, err := runSweep(ctx, services, namespace, all, false, window, since, queryMatch(query))
	if err != nil {
		return nil, err
	}
	return func() { render.Triage(result) }, nil
}

func replayTree(ctx context.Context, services Services, query state.Query) (func(), error) {
	client, err := services.Kubernetes()
	if err != nil {
		return nil, err
	}
	command := TreeCommand{
		Builder: graph.Builder{Client: client}, State: services.State,
		Save: services.State.Save, Match: queryMatch(query),
	}
	namespace, all, _ := recordedScope(query.Args)
	stop := render.Status("resolving ownership graph")
	defer stop()
	switch kind, name, named := strings.Cut(query.Resource, "/"); {
	case named:
		node, err := command.ExecuteResource(ctx, kinds.Kind(kind), name, namespace, true)
		if err != nil {
			return nil, err
		}
		return func() {
			render.Banner(kind, name, namespace, "")
			render.Tree(node)
		}, nil
	case all:
		roots, _, err := command.ExecuteAllNamespaces(ctx, true)
		if err != nil {
			return nil, err
		}
		return func() { printForest(roots, command.Match) }, nil
	default:
		node, err := command.ExecuteNamespace(ctx, namespace, true)
		if err != nil {
			return nil, err
		}
		return func() {
			render.ScopeBanner("Namespace", namespace, "")
			render.Tree(node)
		}, nil
	}
}

// recordedScope reads the scope a sweep or walk recorded: -n with the
// namespace, or -A. The rest of its flags come back with them removed.
func recordedScope(args []string) (namespace string, all bool, rest []string) {
	namespace, rest, _ = extractString(args, "--namespace", "-n")
	all, rest = extractBool(rest, "--all-namespaces", "-A")
	return namespace, all, rest
}

// queryMatch is a query's --match term, empty for none.
func queryMatch(query state.Query) string {
	if query.Match == nil {
		return ""
	}
	return *query.Match
}

// relistCommand is what to run when a listing could not be run again: the
// command that made it, as it would be typed. A kx get listing is named by
// its resource alone — the arguments of a relist are the very names that went
// stale — and a tree of one resource by the listing of its kind, since that
// resource may be the one that went.
func relistCommand(query *state.Query) string {
	if query == nil {
		return "kx get <resource>"
	}
	words := []string{"kx", query.Command}
	switch query.Command {
	case "":
		// A fetch of rows spanning kinds names each row instead, and the
		// listing they came from is not recorded.
		if query.Resource == "" {
			return "kx get <resource>"
		}
		return "kx get " + query.Resource
	case state.CommandFetch:
		// The -A listing its indexes came from, since its own arguments were
		// indexes into that listing.
		if query.Resource == "" {
			return "kx get <resource>"
		}
		return "kx get " + query.Resource + " -A"
	case state.CommandTop:
		if query.Resource == "nodes" {
			words = append(words, "nodes")
		}
	case state.CommandTree:
		if kind, _, named := strings.Cut(query.Resource, "/"); named {
			return kinds.ListCommand(kinds.Kind(kind))
		}
	}
	words = append(words, query.Args...)
	if term := queryMatch(*query); term != "" {
		words = append(words, "-m", term)
	}
	return strings.Join(words, " ")
}

// handleStale reports a failure caused by a vanished resource, then refreshes
// the listing under it.
//
// The error is rendered here rather than left to the entrypoint because the
// fresh listing is what the user picks their next index from, so it has to be
// the last thing on screen. Callers return SilentError so the entrypoint
// doesn't print the same failure a second time.
func handleStale(ctx context.Context, services Services, err error) {
	render.Error(err.Error())
	// Anything but a rendered listing ends with the instruction. Only a
	// successful refresh has an answer on screen already; a replay that failed
	// leaves nothing behind, since its error is discarded with it.
	if outcome, query := recoverState(ctx, services, refreshLead(err)); outcome != refreshed {
		render.Raw("Run '" + relistCommand(query) + "' to refresh the list.")
	}
}

// reportStale is handleStale for a command whose stdout a program reads:
// the failure, and the command that would refresh the listing, on stderr,
// with nothing run again. A refresh nobody sees would also replace the
// listing the user's indexes come from with one they never looked at.
func reportStale(services Services, err error) {
	render.Error(err.Error())
	var query *state.Query
	if current, loadErr := services.State.Load(); loadErr == nil {
		query = current.Query
	}
	render.Notice("Run '" + relistCommand(query) + "' to refresh the list.")
}

// runEach runs act for every resolved reference, continuing past a failure
// that concerns only one of them.
//
// A read asked about several resources should answer for the ones it can.
// kubectl refusing one pod's logs is ordinary in a namespace worth debugging,
// and it used to end the batch: `kx logs 1..2` printed the first deployment's
// error and never reached index 2, which read like the range being exclusive
// rather than like one resource being unreadable. Position decided what you
// saw — `kx logs 2..1` answered for both.
//
// Two kinds of failure still stop everything. An error withRefresh can recover
// from has to reach it, or a stale index would report where it used to relist.
// And an error that is kx's own, rather than kubectl's verdict on one
// resource, says nothing about whether the next resource would fare better.
//
// The first failure's exit code is what the command exits with, so a script
// still notices. kubectl's own message is printed where the failure happened,
// under that resource's banner, unless kubectl already wrote it to the
// terminal itself — which is what SilentError means.
func runEach(resolved []Resolved, act func(target Resolved) error) error {
	var first error
	for _, target := range resolved {
		err := act(target)
		if err == nil {
			continue
		}
		var silent SilentError
		var refused kubectl.Error
		switch {
		case isStale(err):
			// Before kubectl's own verdict: a not-found is both, and
			// withRefresh reports it above the listing it refreshes.
			// Rendered here as well, it printed twice.
			return err
		case errors.As(err, &silent):
			// kubectl streamed its own message already.
		case errors.As(err, &refused):
			render.Error(refused.Error())
		default:
			return err
		}
		if first == nil {
			first = err
		}
	}
	return first
}
