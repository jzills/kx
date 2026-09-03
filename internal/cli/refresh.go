// Stale-state detection and recovery.
//
// When a command fails because its indexed resource no longer exists (pod
// churn), the `kx get` query that produced the current state entry is re-run,
// pushing the fresh list as a new history entry so the user can pick a new
// index. The original command is never retried — the index→name mapping may
// have shifted, so retrying could act on a different resource than the one
// that was asked for.
package cli

import (
	"errors"
	"strings"

	"github.com/jzills/kx/internal/kinds"
	"github.com/jzills/kx/internal/kubectl"
	"github.com/jzills/kx/internal/render"
	"github.com/jzills/kx/internal/state"
)

// StaleResourceError reports that a probe confirmed the indexed resource no
// longer exists.
type StaleResourceError struct {
	Kind kinds.Kind
	Name string
}

func (e StaleResourceError) Error() string {
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
// confusing kubectl message.
func ensureExists(kubectl kubectl.Service, kind kinds.Kind, name, namespace string) error {
	if kubectl.Probe([]string{"get", string(kind), name, "-n", namespace}) != 0 {
		return StaleResourceError{Kind: kind, Name: name}
	}
	return nil
}

// forwardExit turns a non-zero kubectl exit into the error kx should return.
//
// A vanished resource becomes StaleResourceError, so the caller refreshes.
// Anything else forwards kubectl's own exit code: kubectl has already printed
// its message, so there is nothing to add, but exiting 0 would tell a script
// the command succeeded. Returning nil here — which is what ensureExists does
// on its own when the resource is still there — is why `kx describe 1
// --bogus-flag` printed kubectl's error and then exited 0.
func forwardExit(
	kubectl kubectl.Service, kind kinds.Kind, name, namespace string, code int,
) error {
	if err := ensureExists(kubectl, kind, name, namespace); err != nil {
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
		return true
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
	// noQuery: the entry was not created by `kx get` — a tree walk or a triage
	// sweep — so there is nothing to replay, and running a `kx get` is
	// genuinely the way forward.
	noQuery
	// replayFailed: the replay broke on its own terms — most often because the
	// saved query names the very resource that went stale, which is what a
	// relist's query does. Its reason does not reach the screen: Run captures
	// stdout and returns stderr as the error, and that error is dropped here.
	// So this is treated like noQuery, and the caller ends with the same
	// instruction rather than with silence.
	replayFailed
)

// recoverState re-runs the query behind the current state entry and renders the
// fresh listing under lead, which names why it was re-run.
func recoverState(services Services, lead string) recoverOutcome {
	current, err := services.State.Load()
	if err != nil {
		return replayFailed
	}
	if current.Query == nil {
		return noQuery
	}
	query := current.Query
	match := ""
	if query.Match != nil {
		match = *query.Match
	}

	get := GetCommand{Kubectl: services.Kubectl, State: services.State, Index: services.Index}
	table, namespace, err := get.Execute(query.Resource, match, query.Args)
	if err != nil {
		return replayFailed
	}

	render.Raw(lead)
	render.IndexedTable(table, query.Resource, namespace)
	return refreshed
}

// handleStale reports a failure caused by a vanished resource, then refreshes
// the listing under it.
//
// The error is rendered here rather than left to the entrypoint because the
// fresh listing is what the user picks their next index from, so it has to be
// the last thing on screen. Callers return SilentError so the entrypoint
// doesn't print the same failure a second time.
func handleStale(services Services, err error) {
	render.Error(err.Error())
	// Anything but a rendered listing ends with the instruction. Only a
	// successful refresh has an answer on screen already; a replay that failed
	// leaves nothing behind, since Run captures both of kubectl's streams and
	// recoverState discards the error with them.
	if recoverState(services, refreshLead(err)) != refreshed {
		render.Raw("Run 'kx get <resource>' to refresh the list.")
	}
}
