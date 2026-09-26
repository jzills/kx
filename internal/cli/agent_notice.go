package cli

import (
	"fmt"

	"github.com/jzills/kx/internal/kinds"
	"github.com/jzills/kx/internal/render"
)

// mutatingAnnotation marks a command as one whose RunE spends an index that
// can act on the cluster, so installAgentIndexNotice (or a deliberate
// decision not to call it) applies. See agent_notice_test.go's pinning test:
// a command reaching the tree without this annotation, or with the annotation
// but not on the allowlist there, fails it — a new mutating command has to be
// decided one way or the other rather than silently landing on either side.
//
// The annotation arms nothing itself: each command calls
// installAgentIndexNotice from its own RunE, and some only on one path —
// delete and drain only with --yes (without it, the confirm prompt names the
// provenance instead), rollout only for an action that changes the workload.
const mutatingAnnotation = "kx.mutating"

// mutatingAnnotations is the cobra Command.Annotations value every mutating
// command carries. A shared map rather than one literal per command: nothing
// ever writes to a command's Annotations after construction, so the sharing
// is safe, and a single map keeps the marker itself in exactly one place.
var mutatingAnnotations = map[string]string{mutatingAnnotation: "true"}

// installAgentIndexNotice arms services.State to print a stderr notice the
// next time this command resolves an index into a listing an MCP tool made on
// an agent's behalf, before the command acts on it.
//
// A fresh dedup set per call, rather than a package-level one: a command's own
// RunE resolves a ref once (usually to check its namespace against a scope
// flag) and the Execute method underneath it resolves the very same ref again
// to act on it, and both reads reach the same *state.Service.OnAgentIndex.
// Without the dedup, one spent index would print two identical lines. A
// second, distinct tagged index in the same batch — kx cordon 1 2 — still
// gets its own line, because the set is keyed by what was resolved, not by a
// single "already printed" flag.
//
// The key is the whole resolution — index, kind, name and namespace — not the
// index alone. The two reads are two loads of state.json, and a listing saved
// between them (a `kx mcp --write-listings` server, say) can make the same
// index resolve to a different resource the second time. Keyed by index, the
// notice would name the first resource while the command acted on the second;
// keyed by resolution, the changed read prints its own line.
//
// A no-op when services.State is nil: several existing tests build a
// mutating command's cobra.Command over a literal Services{} to exercise pure
// argument validation (a non-numeric replica count, say) that fails before
// any index would ever be resolved, and RunE calls this before that
// validation runs. Arming a State that doesn't exist has nothing to arm.
func installAgentIndexNotice(services Services) {
	if services.State == nil {
		return
	}
	type resolution struct {
		index           int
		kind            kinds.Kind
		name, namespace string
	}
	seen := map[resolution]bool{}
	services.State.OnAgentIndex = func(index int, kind kinds.Kind, name, namespace string) {
		key := resolution{index, kind, name, namespace}
		if seen[key] {
			return
		}
		seen[key] = true
		render.Notice(agentIndexNotice(index, kind, name, namespace))
	}
}

// agentIndexNotice builds the notice sentence. "in <ns>" is omitted for a
// cluster-scoped kind — there is nowhere for the index to have come from — and
// for the (rare) case the entry recorded no namespace at all.
func agentIndexNotice(index int, kind kinds.Kind, name, namespace string) string {
	location := ""
	if namespace != "" {
		if namespaced, known := kinds.Namespaced(kind); !known || namespaced {
			location = " in " + namespace
		}
	}
	return fmt.Sprintf(
		"Index %d is from a kx mcp listing — %s/%s%s. Run 'kx state' to see it.",
		index, kind, name, location)
}
