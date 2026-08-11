package cli

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/jzills/kx/internal/kinds"
	"github.com/jzills/kx/internal/kubectl"
	"github.com/jzills/kx/internal/state"
)

// IndexResolver is the slice of the state service the index-taking commands
// need: turning an index into the resource it names.
type IndexResolver interface {
	Fields(index int) (name, namespace string, kind kinds.Kind, err error)
	// Count returns how many resources are in the current listing, used to
	// resolve the open end of a "5.." range.
	Count() (int, error)
}

// DescribeCommand shows kubectl describe output for an indexed resource.
type DescribeCommand struct {
	Kubectl kubectl.Service
	State   IndexResolver
}

func (c DescribeCommand) Execute(index int, extraArgs []string) error {
	name, namespace, kind, err := c.State.Fields(index)
	if err != nil {
		return err
	}
	args := append([]string{"describe", string(kind), name, "-n", namespace}, extraArgs...)
	code, err := c.Kubectl.RunInteractive(args, false)
	if err != nil {
		return err
	}
	if code != 0 {
		// kubectl already printed its own message; what is left is deciding
		// whether this was a stale index worth refreshing, and forwarding the
		// exit code either way.
		return forwardExit(c.Kubectl, kind, name, namespace, code)
	}
	return nil
}

// EditCommand opens an indexed resource in $EDITOR via kubectl edit.
type EditCommand struct {
	Kubectl kubectl.Service
	State   IndexResolver
}

func (c EditCommand) Execute(index int, extraArgs []string) error {
	name, namespace, kind, err := c.State.Fields(index)
	if err != nil {
		return err
	}
	args := append([]string{"edit", string(kind), name, "-n", namespace}, extraArgs...)
	code, err := c.Kubectl.RunInteractive(args, false)
	if err != nil {
		return err
	}
	if code != 0 {
		return forwardExit(c.Kubectl, kind, name, namespace, code)
	}
	return nil
}

// DeleteCommand deletes an indexed resource, confirming first unless told not to.
type DeleteCommand struct {
	Kubectl kubectl.Service
	State   IndexResolver
	// Confirm asks for consent; it returns an error to abort.
	Confirm func(string) error
	// Status shows a spinner and returns its stop function.
	Status func(string) func()
}

func (c DeleteCommand) Execute(index int, yes bool) (string, error) {
	name, namespace, kind, err := c.State.Fields(index)
	if err != nil {
		return "", err
	}
	// The prompt must stay outside the spinner: a prompt underneath a
	// repainting status line cannot be read.
	if !yes {
		if err := c.Confirm(fmt.Sprintf("Delete %s/%s in %s?", kind, name, namespace)); err != nil {
			return "", err
		}
	}
	stop := c.Status("deleting")
	_, err = c.Kubectl.Run([]string{"delete", string(kind), name, "-n", namespace})
	stop()
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("Deleted %s/%s", kind, name), nil
}

var scalableKinds = map[kinds.Kind]bool{
	kinds.Deployment: true, kinds.StatefulSet: true, kinds.ReplicaSet: true,
}

// ScaleCommand sets the replica count on an indexed workload.
type ScaleCommand struct {
	Kubectl kubectl.Service
	State   IndexResolver
}

func (c ScaleCommand) Execute(index, replicas int) (string, error) {
	name, namespace, kind, err := c.State.Fields(index)
	if err != nil {
		return "", err
	}
	if !scalableKinds[kind] {
		return "", fmt.Errorf("scale is not supported for '%s'.", kind)
	}
	_, err = c.Kubectl.Run([]string{
		"scale", string(kind) + "/" + name,
		"--replicas=" + strconv.Itoa(replicas), "-n", namespace,
	})
	if err != nil {
		return "", err
	}
	noun := "replicas"
	if replicas == 1 {
		noun = "replica"
	}
	return fmt.Sprintf("Scaled %s/%s to %d %s", kind, name, replicas, noun), nil
}

var rolloutKinds = map[kinds.Kind]bool{
	kinds.Deployment: true, kinds.StatefulSet: true, kinds.DaemonSet: true,
}

// rolloutActions are the kubectl rollout subcommands kx exposes, in the order
// help and completion list them. `status` blocks until the rollout settles, so
// it streams rather than being captured.
//
// One ordered list rather than a set, because the same six names are the
// command's validation, its help text and its shell completion, and three
// copies of them drift.
var rolloutActions = []struct{ Name, Doc string }{
	{"status", "Show the rollout status"},
	{"restart", "Restart the workload"},
	{"pause", "Pause the rollout"},
	{"resume", "Resume a paused rollout"},
	{"history", "Show the revision history"},
	{"undo", "Roll back to the previous revision"},
}

func isRolloutAction(action string) bool {
	for _, candidate := range rolloutActions {
		if candidate.Name == action {
			return true
		}
	}
	return false
}

// rolloutActionNames lists the actions for prose: help text and errors.
func rolloutActionNames() []string {
	names := make([]string, 0, len(rolloutActions))
	for _, action := range rolloutActions {
		names = append(names, action.Name)
	}
	return names
}

var interactiveRolloutActions = map[string]bool{"status": true}

// RolloutCommand drives kubectl rollout for an indexed workload.
type RolloutCommand struct {
	Kubectl kubectl.Service
	State   IndexResolver
}

// Execute returns the captured output, or "" for actions that stream directly.
func (c RolloutCommand) Execute(action string, index int) (string, error) {
	if !isRolloutAction(action) {
		return "", fmt.Errorf("unknown rollout action '%s'.", action)
	}
	name, namespace, kind, err := c.State.Fields(index)
	if err != nil {
		return "", err
	}
	if !rolloutKinds[kind] {
		return "", fmt.Errorf("rollout is not supported for '%s'.", kind)
	}
	args := []string{"rollout", action, string(kind) + "/" + name, "-n", namespace}
	if interactiveRolloutActions[action] {
		code, err := c.Kubectl.RunInteractive(args, false)
		if err != nil {
			return "", err
		}
		if code != 0 {
			return "", forwardExit(c.Kubectl, kind, name, namespace, code)
		}
		return "", nil
	}
	return c.Kubectl.Run(args)
}

var portForwardKinds = map[kinds.Kind]bool{
	kinds.Pod: true, kinds.Deployment: true, kinds.ReplicaSet: true,
	kinds.StatefulSet: true, kinds.DaemonSet: true, kinds.Service: true,
}

// PortForwardCommand forwards a local port to an indexed resource.
type PortForwardCommand struct {
	Kubectl kubectl.Service
	State   IndexResolver
}

func (c PortForwardCommand) Execute(index int, port string, extraArgs []string) error {
	name, namespace, kind, err := c.State.Fields(index)
	if err != nil {
		return err
	}
	if !portForwardKinds[kind] {
		return fmt.Errorf("port-forward is not supported for '%s'.", kind)
	}
	args := append([]string{
		"port-forward", string(kind) + "/" + name, port, "-n", namespace,
	}, extraArgs...)
	code, err := c.Kubectl.RunInteractive(args, false)
	if err != nil {
		return err
	}
	if code != 0 {
		return forwardExit(c.Kubectl, kind, name, namespace, code)
	}
	return nil
}

// CopyCommand copies files to or from an indexed pod via kubectl cp.
type CopyCommand struct {
	Kubectl kubectl.Service
	State   IndexResolver
}

// resolvedPod is what resolve found, if the argument named an indexed pod —
// nil when it didn't, which is the common case for the local-path side of
// a copy.
type resolvedPod struct {
	Name      string
	Namespace string
}

func (c CopyCommand) Execute(src, dest string, extraArgs []string) error {
	src, srcPod, err := c.resolve(src)
	if err != nil {
		return err
	}
	dest, destPod, err := c.resolve(dest)
	if err != nil {
		return err
	}
	args := append([]string{"cp", src, dest}, extraArgs...)
	code, err := c.Kubectl.RunInteractive(args, false)
	if err != nil {
		return err
	}
	if code == 0 {
		return nil
	}
	// cp copies between local and exactly one pod, never pod-to-pod, so at
	// most one side ever resolves — whichever one did is what's worth
	// checking for staleness.
	pod := srcPod
	if pod == nil {
		pod = destPod
	}
	if pod == nil {
		return SilentError{Code: code}
	}
	return forwardExit(c.Kubectl, kinds.Pod, pod.Name, pod.Namespace, code)
}

// resolve rewrites an "<index>:<path>" argument into kubectl cp's own
// "<namespace>/<pod>:<path>" pod-reference syntax. Anything that doesn't
// parse as "<int>:..." passes through completely unchanged — a local path,
// or an already-qualified ns/pod:path for bypassing the index entirely.
func (c CopyCommand) resolve(arg string) (rewritten string, pod *resolvedPod, err error) {
	before, path, found := strings.Cut(arg, ":")
	if !found {
		return arg, nil, nil
	}
	index, err := strconv.Atoi(before)
	if err != nil {
		return arg, nil, nil
	}
	name, namespace, kind, err := c.State.Fields(index)
	if err != nil {
		return "", nil, err
	}
	if kind != kinds.Pod {
		return "", nil, fmt.Errorf("cp is only supported for pods.")
	}
	return namespace + "/" + name + ":" + path, &resolvedPod{Name: name, Namespace: namespace}, nil
}

// Kinds whose logs are aggregated across the pods they own, rather than read
// from a single pod.
var aggregateLogKinds = map[kinds.Kind]bool{
	kinds.Deployment: true, kinds.StatefulSet: true,
	kinds.DaemonSet: true, kinds.Service: true,
}

// LogsCommand streams logs for an indexed resource.
type LogsCommand struct {
	Kubectl kubectl.Service
	State   IndexResolver
	Status  func(string) func()
}

func (c LogsCommand) Execute(index int, extraArgs []string) error {
	name, namespace, kind, err := c.State.Fields(index)
	if err != nil {
		return err
	}

	switch {
	case kind == kinds.Pod:
		args := append([]string{"logs", name, "-n", namespace}, extraArgs...)
		code, err := c.Kubectl.RunInteractive(args, false)
		if err != nil {
			return err
		}
		if code != 0 {
			return forwardExit(c.Kubectl, kind, name, namespace, code)
		}
		return nil

	case aggregateLogKinds[kind]:
		selector, err := c.selector(name, namespace, kind)
		if err != nil {
			return err
		}
		args := append([]string{
			"logs", "-l", selector, "--prefix=true", "-n", namespace,
		}, extraArgs...)
		code, err := c.Kubectl.RunInteractive(args, false)
		if err != nil {
			return err
		}
		if code != 0 {
			// No per-resource staleness check: the selector may legitimately
			// match nothing, and the workload itself was just read to build it.
			return SilentError{Code: code}
		}
		return nil

	default:
		return fmt.Errorf("logs are not supported for '%s'.", kind)
	}
}

// selector builds the label selector that matches a workload's pods.
func (c LogsCommand) selector(name, namespace string, kind kinds.Kind) (string, error) {
	stop := c.Status("resolving pod selector")
	raw, err := c.Kubectl.Run([]string{"get", string(kind), name, "-n", namespace, "-o", "json"})
	stop()
	if err != nil {
		return "", err
	}

	var object struct {
		Spec struct {
			// A Service selects pods directly; a workload selects them through
			// its template's matchLabels.
			Selector json.RawMessage `json:"selector"`
		} `json:"spec"`
	}
	if err := json.Unmarshal([]byte(raw), &object); err != nil {
		return "", err
	}

	labels := map[string]string{}
	if kind == kinds.Service {
		_ = json.Unmarshal(object.Spec.Selector, &labels)
	} else {
		var selector struct {
			MatchLabels map[string]string `json:"matchLabels"`
		}
		_ = json.Unmarshal(object.Spec.Selector, &selector)
		labels = selector.MatchLabels
	}
	if len(labels) == 0 {
		return "", fmt.Errorf("%s/%s has no pod selector; cannot aggregate logs.", kind, name)
	}

	// Sorted so the selector is stable run to run, which matters when it shows
	// up in shell history and bug reports.
	pairs := make([]string, 0, len(labels))
	for key, value := range labels {
		pairs = append(pairs, key+"="+value)
	}
	sortStrings(pairs)
	return strings.Join(pairs, ","), nil
}

// ExecCommand runs a command, or an interactive shell, inside an indexed pod.
type ExecCommand struct {
	Kubectl kubectl.Service
	State   IndexResolver
	// Shells are tried in order when no explicit command is given.
	Shells []string
}

func (c ExecCommand) Execute(index int, command, extraArgs []string) error {
	name, namespace, kind, err := c.State.Fields(index)
	if err != nil {
		return err
	}
	if kind != kinds.Pod {
		return fmt.Errorf("exec is only supported for pods.")
	}

	if len(command) > 0 {
		args := append([]string{"exec", "-it", name, "-n", namespace}, extraArgs...)
		args = append(args, "--")
		args = append(args, command...)
		// kubectl's own stderr is suppressed: a failing command inside the
		// container produces a confusing "command terminated with exit code N"
		// on top of whatever the command itself printed.
		code, err := c.Kubectl.RunInteractive(args, true)
		if err != nil {
			return err
		}
		if code != 0 {
			if err := ensureExists(c.Kubectl, kinds.Pod, name, namespace); err != nil {
				return err
			}
			// The message has to be kx's own — kubectl's stderr is suppressed
			// above — but the code belongs to the command that ran, so that
			// `kx exec 1 -- test -f /x` is usable from a shell.
			return ExitError{
				Code:    code,
				Message: fmt.Sprintf("Command failed in container (exit %d).", code),
			}
		}
		return nil
	}

	for _, shell := range c.Shells {
		probe := append([]string{"exec", name, "-n", namespace}, extraArgs...)
		probe = append(probe, "--", shell, "-c", "exit 0")
		if c.Kubectl.Probe(probe) != 0 {
			continue
		}
		args := append([]string{"exec", "-it", name, "-n", namespace}, extraArgs...)
		args = append(args, "--", shell)
		_, err := c.Kubectl.RunInteractive(args, false)
		return err
	}

	if err := ensureExists(c.Kubectl, kinds.Pod, name, namespace); err != nil {
		return err
	}
	return fmt.Errorf(
		"No shell found in container. Provide an explicit command: kx exec <index> -- /path/to/binary",
	)
}

// ContextsCommand lists kubeconfig contexts and indexes them.
//
// The listing goes to the context slot and nowhere else. A context is a
// kubeconfig entry rather than a server resource — there is no
// `kubectl describe context` — so nothing that reads the history stack can do
// anything with one, and pushing it there only evicts work the stack is for.
type ContextsCommand struct {
	Kubectl kubectl.Service
	State   NamedStateWriter
	Index   Indexer
}

// Execute lists the contexts and returns the indexed table along with the active
// context, which captions it.
//
// The context is returned rather than left for the caller to fetch, the way
// GetCommand.Execute returns its namespace: the listing is saved with it
// already, and `kubectl config current-context` is a subprocess worth spawning
// once. Reading it back out of state is not an option either — the listing goes
// to the slot, not the history the caller can Load().
func (c ContextsCommand) Execute() (table, context string, err error) {
	output, err := c.Kubectl.Run([]string{"config", "get-contexts"})
	if err != nil {
		return "", "", err
	}
	current := c.Kubectl.CurrentContext()
	indexed, names := c.Index.Add(output)
	if len(names) > 0 {
		if err := c.State.SaveNamed(state.State{
			Resources: state.NewResources(names, kinds.Context),
			Namespace: current,
		}); err != nil {
			return "", "", err
		}
	}
	return indexed, current, nil
}

// NamedResolver resolves an index against a kind's own slot rather than against
// the history cursor.
type NamedResolver interface {
	FieldsNamed(index int, kind kinds.Kind) (name, namespace string, err error)
}

// SwitchCommand activates an indexed namespace or context.
//
// The index is resolved against the slot for the kind the command names, not
// against the current history entry. `kx ns 2` has already said which kind it
// means, so an intervening `kx get pods` should not turn that 2 into a pod —
// which is what made the sequence `kx ns` / `kx get pod` / `kx ns 2` fail (#156).
//
// Reading the slot is also what keeps a stale index from making a pod's name the
// active namespace: `kubectl config set-context --namespace` accepts any string
// and validates nothing against the server, and only namespace listings ever
// reach the namespace slot. There is deliberately no fallback to the current
// entry when the slot is empty — that fallback is the defect.
//
// Nothing here checks the namespace exists either. Setting one is a local
// kubeconfig edit that kubectl does not validate — pointing at a namespace
// before creating it is a normal thing to do — and kx pre-empts nothing
// elsewhere: every staleness check in the tool reacts to a failure rather than
// running ahead of one.
type SwitchCommand struct {
	Kubectl kubectl.Service
	State   NamedResolver
}

func (c SwitchCommand) namespace(index int) (string, error) {
	name, _, err := c.State.FieldsNamed(index, kinds.Namespace)
	if err != nil {
		return "", err
	}
	_, err = c.Kubectl.Run([]string{"config", "set-context", "--current", "--namespace=" + name})
	return name, err
}

func (c SwitchCommand) context(index int) (string, error) {
	name, _, err := c.State.FieldsNamed(index, kinds.Context)
	if err != nil {
		return "", err
	}
	// use-context rejects a name that isn't there with a message of its own,
	// which is the whole of the validation either switch needs.
	_, err = c.Kubectl.Run([]string{"config", "use-context", name})
	return name, err
}
