package cli

import (
	"strings"

	"github.com/jzills/kx/internal/kinds"
	"github.com/jzills/kx/internal/kubectl"
	"github.com/jzills/kx/internal/state"
)

// Indexer prefixes kubectl output with an index column and filters it by name.
type Indexer interface {
	Add(output string) (string, []string)
	Filter(output, term string) string
}

// StateWriter is the slice of the state service `get` needs.
type StateWriter interface {
	Save(state.State) error
}

// GetCommand lists resources and saves the listing so later commands can
// resolve indexes against it.
type GetCommand struct {
	Kubectl kubectl.Service
	State   StateWriter
	Index   Indexer
}

// extractNamespace finds an explicit namespace in the pass-through flags, so
// the saved state records the namespace the listing actually came from rather
// than the context's current one.
func extractNamespace(extraArgs []string) string {
	for i, arg := range extraArgs {
		if (arg == "-n" || arg == "--namespace") && i+1 < len(extraArgs) {
			return extraArgs[i+1]
		}
		if strings.HasPrefix(arg, "--namespace=") {
			return strings.SplitN(arg, "=", 2)[1]
		}
	}
	return ""
}

func allNamespaces(extraArgs []string) bool {
	for _, arg := range extraArgs {
		if arg == "-A" || arg == "--all-namespaces" {
			return true
		}
	}
	return false
}

// Execute runs `kubectl get`, indexes the output and persists it. It returns
// the text to display.
func (c GetCommand) Execute(resource, filterTerm string, extraArgs []string) (string, error) {
	output, err := c.Kubectl.Run(append([]string{"get", resource}, extraArgs...))
	if err != nil {
		return "", err
	}
	if filterTerm != "" {
		output = c.Index.Filter(output, filterTerm)
	}
	if allNamespaces(extraArgs) {
		// Names aren't unique across namespaces, so `-A` results are never
		// indexed — returning unindexed output keeps dead X numbers off the
		// screen.
		return output, nil
	}

	indexed, names := c.Index.Add(output)
	if len(names) > 0 {
		namespace := extractNamespace(extraArgs)
		if namespace == "" {
			namespace = c.Kubectl.CurrentNamespace()
		}
		var match *string
		if filterTerm != "" {
			match = &filterTerm
		}
		if extraArgs == nil {
			extraArgs = []string{}
		}
		entry := state.State{
			Resources: state.NewResources(names, kinds.Normalize(resource)),
			Namespace: namespace,
			Query: &state.Query{
				Resource: resource,
				Args:     extraArgs,
				Match:    match,
			},
		}
		if err := c.State.Save(entry); err != nil {
			return "", err
		}
	}
	return indexed, nil
}
