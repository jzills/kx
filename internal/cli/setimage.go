package cli

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/jzills/kx/internal/kinds"
	"github.com/jzills/kx/internal/kubectl"
	"github.com/jzills/kx/internal/render"
)

// imageKinds are the kinds kubectl set image accepts that kx can name.
//
// kubectl's list, less ReplicationController, which kx has no kind for. A Job
// is absent from kubectl's list too: its pod template is immutable, so the
// refusal kx gives before any call is the one kubectl would give after it.
var imageKinds = kinds.Set{
	kinds.Pod, kinds.Deployment, kinds.StatefulSet, kinds.DaemonSet,
	kinds.ReplicaSet, kinds.CronJob,
}

// SetImageCommand changes the container images of one resolved workload.
type SetImageCommand struct {
	Kubectl kubectl.Service
}

// imageChange is one container's image before and after, read off the
// workload before kubectl changes it — the line kx prints is the only record
// of what was replaced, which is exactly what putting it back needs.
type imageChange struct {
	Container, From, To string
}

// container is one entry of a pod spec: its name and the image it runs.
type container struct {
	Name, Image string
}

// Execute reads the workload's containers, turns specs into kubectl's
// name=image pairs, and applies them. Every refusal happens before
// kubectl set image runs, so a bad spec changes nothing.
//
// specs are what was typed after the index: one bare image for a workload with
// a single container, or any number of name=image pairs, where * names every
// container, as kubectl spells it.
func (c SetImageCommand) Execute(
	target Resolved, specs, extraArgs []string,
) ([]imageChange, error) {
	if !imageKinds.Has(target.Kind) {
		return nil, unsupportedKindError("set image", target.Kind, imageKinds)
	}
	subject := string(target.Kind) + "/" + target.Name
	raw, err := c.Kubectl.Run([]string{
		"get", subject, "-n", target.Namespace, "-o", "json",
	})
	if err != nil {
		return nil, err
	}
	var object map[string]json.RawMessage
	if err := json.Unmarshal([]byte(raw), &object); err != nil {
		return nil, err
	}
	containers := containersOf(object)

	pairs, changes, err := imagePairs(subject, target.Ref.String(), containers, specs)
	if err != nil {
		return nil, err
	}
	args := append([]string{"set", "image", subject}, pairs...)
	args = append(args, "-n", target.Namespace)
	if _, err := c.Kubectl.Run(append(args, extraArgs...)); err != nil {
		return nil, err
	}
	return changes, nil
}

// imagePairs builds kubectl's name=image arguments and the change each one
// makes.
//
// A bare image is accepted only alone and only for a workload with one
// container, init containers counted: kubectl matches those by name too, so a
// pod with one container and one init container has two things a bare image
// could mean. An image reference never contains '=', so a spec is a pair
// exactly when it has one.
//
// ref is the reference as typed, so a refusal's suggested command is one the
// caller can run as it stands.
func imagePairs(
	subject, ref string, containers []container, specs []string,
) ([]string, []imageChange, error) {
	names := make([]string, 0, len(containers))
	byName := map[string]container{}
	for _, each := range containers {
		names = append(names, each.Name)
		byName[each.Name] = each
	}
	nameOne := func(image string) error {
		return fmt.Errorf("%s has containers %s — name one: 'kx set image %s %s=%s'.",
			subject, strings.Join(names, ", "), ref, names[0], image)
	}

	var pairs []string
	var changes []imageChange
	for _, spec := range specs {
		name, image, paired := strings.Cut(spec, "=")
		if !paired {
			image = spec
			if len(specs) > 1 {
				return nil, nil, fmt.Errorf(
					"'%s' names no container, and a bare image can only stand alone — "+
						"with more than one change, name each: 'name=image'.", spec)
			}
			if len(containers) == 0 {
				return nil, nil, fmt.Errorf("%s has no containers to set an image on.", subject)
			}
			if len(containers) > 1 {
				return nil, nil, nameOne(image)
			}
			name = containers[0].Name
		}
		if name == "" || image == "" {
			return nil, nil, fmt.Errorf(
				"'%s' is not a container=image pair — both sides are needed.", spec)
		}
		pairs = append(pairs, name+"="+image)
		if name == "*" {
			for _, each := range containers {
				changes = append(changes, imageChange{each.Name, each.Image, image})
			}
			continue
		}
		current, ok := byName[name]
		if !ok {
			return nil, nil, fmt.Errorf("%s has no container named '%s' — it has %s.",
				subject, name, strings.Join(names, ", "))
		}
		changes = append(changes, imageChange{name, current.Image, image})
	}
	return pairs, changes, nil
}

// containersOf reads a workload's init containers and containers, in that
// order, from the same pod spec kx scan reads images from.
func containersOf(object map[string]json.RawMessage) []container {
	spec := podSpec(object)
	var containers []container
	for _, group := range []string{"initContainers", "containers"} {
		var entries []container
		if raw, ok := spec[group]; ok {
			_ = json.Unmarshal(raw, &entries)
		}
		containers = append(containers, entries...)
	}
	return containers
}

// imageChangeLine is one container's result, in the order the scope banner
// reads: what, where, then the change itself.
func imageChangeLine(target Resolved, change imageChange, dryRun bool) string {
	result := change.Container + ": " + change.From + " → " + change.To
	if change.From == change.To {
		result = change.Container + ": already " + change.To
	}
	// Every kind set image accepts is namespaced, so the namespace is always
	// there to name.
	line := string(target.Kind) + "/" + target.Name + " · " + target.Namespace + " · " + result
	if dryRun {
		line += " (dry run — nothing was changed)"
	}
	return line
}

func newSetCommand(services Services) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "set",
		Short: "Change an indexed workload's container images with kx set image, printing each one before and after.",
		// The site reference and the README table document a group from its
		// own text, not its subcommands', and image is the only one — so the
		// group carries its usage and examples.
		Long: "Changes a field of an indexed workload in place, the way kubectl set does. " +
			"Only image is wrapped: 'kubectl set env' and the rest can take an index " +
			"through kx ref.\n\n" +
			"`kx set image <index> <image>...` changes a Pod, Deployment, StatefulSet, " +
			"DaemonSet, ReplicaSet or CronJob's container images and prints each one " +
			"before and after, so the line says what to type to put it back. A bare " +
			"image sets the workload's only container; with more than one, init " +
			"containers included, name each — api=api:v2 — or use * for every one. " +
			"kubectl's own flags pass through.",
		Example: "  kx set image 1 nginx:1.27.3\n" +
			"  kx set image 1 api=api:v2 envoy=envoyproxy/envoy:v1.31\n" +
			"  kx set image @api api:v2 --dry-run=server",
		// Every subcommand changes the cluster, so the group is what the
		// pinning test sees as mutating. Like the annotation everywhere else it
		// arms nothing — image's RunE arms the agent notice itself.
		Annotations: mutatingAnnotations,
	}
	// Wrapped here rather than at the root: withRefresh wraps a RunE, and
	// the group has none. A stale index then relists as it does for scale.
	cmd.AddCommand(withRefresh(services, newSetImageCommand(services)))
	return cmd
}

func newSetImageCommand(services Services) *cobra.Command {
	return &cobra.Command{
		Use: "image <index> <image>... [kubectl flags]",
		Short: "Change the container images of an indexed Pod, Deployment, StatefulSet, " +
			"DaemonSet, ReplicaSet or CronJob.",
		Long: "Changes the container images of an indexed workload and prints each one " +
			"before and after, so the line says what to type to put it back.\n\n" +
			"A bare image sets the workload's only container. With more than one — init " +
			"containers included — name each: api=api:v2. * sets every container, as " +
			"kubectl spells it.\n\n" +
			"A Deployment, StatefulSet or DaemonSet rolls out the change; kx rollout status " +
			"on the same index follows it, and kx rollout undo reverts it.\n\n" +
			"kubectl's own flags pass through — --dry-run=server to check the change is " +
			"accepted without making it.\n\n" +
			"Unrecognized flags are passed through to kubectl.",
		Example: "  kx set image 1 nginx:1.27.3\n" +
			"  kx set image 1 api=api:v2 envoy=envoyproxy/envoy:v1.31\n" +
			"  kx set image 1 '*=api:v2'\n" +
			"  kx set image @api api:v2 --dry-run=server",
		// No Args validator, for the reason scale has none: cobra counts the
		// forwarded kubectl flags as arguments. The arity check is below.
		// No annotation either: the pinning test reads top-level commands,
		// and the set group carries it for this one.
		DisableFlagParsing: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			rest, handled, err := passthrough(cmd, args, nil)
			if err != nil || handled {
				return err
			}
			// The specs are the words before the first flag: an image never
			// starts with a dash, and anything that does is kubectl's.
			specs := 0
			for specs+1 < len(rest) && !strings.HasPrefix(rest[specs+1], "-") {
				specs++
			}
			if len(rest) < 2 || specs == 0 {
				return requiredArgsError(cmd)
			}
			installAgentIndexNotice(services)
			ref, err := parseRef("index", rest[0])
			if err != nil {
				return err
			}
			extra := rest[1+specs:]
			name, namespace, kind, err := services.State.Resolve(ref)
			if err != nil {
				return err
			}
			target := Resolved{Ref: ref, Name: name, Namespace: namespace, Kind: kind}
			if err := refuseScopeFlagResolved([]Resolved{target}, extra); err != nil {
				return err
			}
			changes, err := SetImageCommand{Kubectl: services.Kubectl}.
				Execute(target, rest[1:1+specs], extra)
			if err != nil {
				return err
			}
			dryRun := isDryRun(extra)
			changed := false
			for _, change := range changes {
				render.Success(imageChangeLine(target, change, dryRun))
				changed = changed || change.From != change.To
			}
			// Only a real change starts a rollout: a dry run makes none, and
			// nor does setting every container to the image it already runs.
			if rolloutKinds.Has(kind) && changed && !dryRun {
				render.Caption("kx rollout status " + ref.String() + " to follow it")
			}
			return nil
		},
	}
}
