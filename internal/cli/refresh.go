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

// IsNotFound reports whether an error looks like a missing resource.
func IsNotFound(err error) bool {
	if err == nil {
		return false
	}
	message := err.Error()
	for _, marker := range notFoundMarkers {
		if strings.Contains(message, marker) {
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
// describes the cluster.
func isStale(err error) bool {
	var stale StaleResourceError
	if errors.As(err, &stale) {
		return true
	}
	return IsNotFound(err)
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
	// replayFailed: the replay broke on its own terms. Whatever went wrong is
	// already on screen, and pointing the user at the command that just failed
	// would only add noise.
	replayFailed
)

// recoverState re-runs the query behind the current state entry and renders the
// fresh listing.
func recoverState(services Services) recoverOutcome {
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
	table, err := get.Execute(query.Resource, match, query.Args)
	if err != nil {
		return replayFailed
	}
	updated, err := services.State.Load()
	if err != nil {
		return replayFailed
	}

	render.Raw("State was stale — refreshed, pick a new index:")
	render.IndexedTable(table, query.Resource, updated.Namespace, "")
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
	if recoverState(services) == noQuery {
		render.Raw("Run 'kx get <resource>' to refresh the list.")
	}
}
