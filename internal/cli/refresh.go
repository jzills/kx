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

// isStale reports whether an error means the current state entry no longer
// describes the cluster.
func isStale(err error) bool {
	var stale StaleResourceError
	if errors.As(err, &stale) {
		return true
	}
	return IsNotFound(err)
}

// recover re-runs the query behind the current state entry and renders the
// fresh listing. Returns false when there is nothing to refresh from — a state
// entry not created by `kx get` has no query to replay.
func recoverState(services Services) bool {
	current, err := services.State.Load()
	if err != nil || current.Query == nil {
		return false
	}
	query := current.Query
	match := ""
	if query.Match != nil {
		match = *query.Match
	}

	get := GetCommand{Kubectl: services.Kubectl, State: services.State, Index: services.Index}
	table, err := get.Execute(query.Resource, match, query.Args)
	if err != nil {
		return false
	}
	refreshed, err := services.State.Load()
	if err != nil {
		return false
	}

	render.Raw("State was stale — refreshed, pick a new index:")
	render.IndexedTable(table, query.Resource, refreshed.Namespace, "")
	return true
}

// handleStale is called after a command fails. It renders the refreshed listing
// when the failure means the index pointed at something that no longer exists.
func handleStale(services Services, err error) {
	if !isStale(err) {
		return
	}
	if !recoverState(services) {
		render.Raw("Run 'kx get <resource>' to refresh the list.")
	}
}
