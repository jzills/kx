package cli

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/jzills/kx/internal/render"
)

// RefCommand formats already-resolved references into the lines a shell can
// spend.
//
// It is the escape hatch for everything kx does not wrap. kx implements two
// dozen of kubectl's verbs, kubectl has twice that, and the ecosystem around
// it — stern, velero, kubectl-neat, istioctl — has no end; without a way to
// get a resource's identity back out, an index could only be spent on the
// commands kx had already been taught. Every unwrapped verb was a feature
// request. This answers all of them at once.
//
// Deliberately offline: it holds no kubectl service and makes no call, so it
// is instant and works with no connectivity. It reports what an index *means*,
// not what still exists — a stale index prints the name it was assigned, and
// the command the caller then runs is what discovers the resource is gone.
type RefCommand struct{}

// Field names, as the flags spell them. The empty field is the default: the
// whole reference, as a kubectl argument fragment.
const (
	refFieldName      = "name"
	refFieldNamespace = "namespace"
	refFieldKind      = "kind"
)

// Execute formats each already-resolved reference to one line.
//
// Takes []Resolved rather than resolving indexes itself, so a bad reference
// anywhere in the batch is caught by resolveRefs before any line is formatted
// — kx ref is read-only, so a partial list isn't destructive, but handing a
// caller half the references and a non-zero exit is worse than handing it
// none.
//
// Lines rather than printed output so the shape is testable as data, and so
// the caller decides how they reach the terminal.
func (c RefCommand) Execute(resolved []Resolved, field string) ([]string, error) {
	lines := make([]string, 0, len(resolved))
	for _, target := range resolved {
		index, name, namespace, kind := target.Ref.Index, target.Name, target.Namespace, target.Kind
		// Lowercased canonical kind, not kubectl's shorthand: `rs` and
		// `deploy` are kubectl's own spellings, and this exists to compose
		// with tools that are not kubectl. Lowercase because `pod/x` is the
		// conventional spelling, and kubectl accepts either.
		spelling := strings.ToLower(string(kind))
		switch field {
		case refFieldName:
			lines = append(lines, name)
		case refFieldKind:
			lines = append(lines, spelling)
		case refFieldNamespace:
			// An empty line would hand `-n $(kx ref 1 --namespace)` a bare
			// flag with no value, and kubectl fails somewhere further from
			// the cause than here.
			if namespace == "" {
				return nil, fmt.Errorf(
					"Index %d is %s/%s, which lives outside any namespace — there is no namespace to print.",
					index, kind, name)
			}
			lines = append(lines, namespace)
		default:
			reference := spelling + "/" + name
			// No -n for a cluster-scoped resource: the flag is wrong there
			// rather than redundant, and kubectl accepts it silently.
			if namespace != "" {
				reference += " -n " + namespace
			}
			lines = append(lines, reference)
		}
	}
	return lines, nil
}

func newRefCommand(services Services) *cobra.Command {
	var name, namespace, kind bool
	cmd := &cobra.Command{
		Use:   "ref <index>...",
		Short: "Print what an index refers to, for commands kx doesn't wrap.",
		Long: "Prints the resource an index refers to as a kubectl argument fragment, one " +
			"line per index, so an index can be spent on anything — a kubectl verb kx " +
			"doesn't wrap, or another tool entirely.\n\n" +
			"--name, --namespace and --kind print that field alone, for tools that take " +
			"the pieces separately. They are mutually exclusive: one field per line means " +
			"a caller always knows how many words a line holds.\n\n" +
			"Nothing here touches the cluster. It reports what the index means, not what " +
			"still exists — the command you spend it on is what finds out.",
		Args: minArgs(1),
		Example: "  kx ref 3\n" +
			"  kubectl exec $(kx ref 3) -- sh\n" +
			"  kubectl get $(kx ref 1..3)\n" +
			"  stern $(kx ref 3 --name) -n $(kx ref 3 --namespace)",
		RunE: func(cmd *cobra.Command, args []string) error {
			field, err := refField(name, namespace, kind)
			if err != nil {
				return err
			}
			resolved, err := resolveRefs(services.State, "indexes", args)
			if err != nil {
				return err
			}
			lines, err := RefCommand{}.Execute(resolved, field)
			if err != nil {
				return err
			}
			for _, line := range lines {
				render.Raw(line)
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&name, refFieldName, false, "Print only the resource name")
	cmd.Flags().BoolVar(&namespace, refFieldNamespace, false, "Print only the namespace")
	cmd.Flags().BoolVar(&kind, refFieldKind, false, "Print only the kind")
	return cmd
}

// refField picks the one field asked for, refusing a combination.
//
// Two fields on one line would be a third output format nobody asked for, and
// silently honouring the first flag would make `kx ref 3 --name --namespace`
// print something that looks right and isn't.
func refField(name, namespace, kind bool) (string, error) {
	chosen := make([]string, 0, 3)
	for _, candidate := range []struct {
		set  bool
		name string
	}{{name, refFieldName}, {namespace, refFieldNamespace}, {kind, refFieldKind}} {
		if candidate.set {
			chosen = append(chosen, "--"+candidate.name)
		}
	}
	if len(chosen) > 1 {
		return "", fmt.Errorf(
			"%s print different fields; pick one, or drop both for the whole reference.",
			strings.Join(chosen, " and "))
	}
	if len(chosen) == 0 {
		return "", nil
	}
	return strings.TrimPrefix(chosen[0], "--"), nil
}
