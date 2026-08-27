package cli

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/jzills/kx/internal/index"
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

// execKinds are the kinds kubectl exec accepts. Deliberately not Service:
// kubectl resolves a Service to an endpoint for port-forward but has no
// equivalent for exec, so kx refuses it here rather than letting kubectl
// produce a worse message.
var execKinds = map[kinds.Kind]bool{
	kinds.Pod: true, kinds.Deployment: true, kinds.ReplicaSet: true,
	kinds.StatefulSet: true, kinds.DaemonSet: true,
}

// execTarget is how an indexed resource is named to kubectl exec.
//
// A Pod is addressed bare, which is what every other pod-only command already
// sends and what kubectl has always taken. A workload is addressed TYPE/NAME,
// and kubectl picks one of its pods — the same delegation PortForwardCommand
// already relies on rather than resolving pods itself.
func execTarget(kind kinds.Kind, name string) string {
	if kind == kinds.Pod {
		return name
	}
	return string(kind) + "/" + name
}

func (c ExecCommand) Execute(index int, command, extraArgs []string) error {
	name, namespace, kind, err := c.State.Fields(index)
	if err != nil {
		return err
	}
	if !execKinds[kind] {
		return fmt.Errorf("exec is not supported for '%s'.", kind)
	}
	target := execTarget(kind, name)

	if len(command) > 0 {
		args := append([]string{"exec", "-it", target, "-n", namespace}, extraArgs...)
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
			if err := ensureExists(c.Kubectl, kind, name, namespace); err != nil {
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
		probe := append([]string{"exec", target, "-n", namespace}, extraArgs...)
		probe = append(probe, "--", shell, "-c", "exit 0")
		if c.Kubectl.Probe(probe) != 0 {
			continue
		}
		args := append([]string{"exec", "-it", target, "-n", namespace}, extraArgs...)
		args = append(args, "--", shell)
		_, err := c.Kubectl.RunInteractive(args, false)
		return err
	}

	if err := ensureExists(c.Kubectl, kind, name, namespace); err != nil {
		return err
	}
	return fmt.Errorf(
		"No shell found in container. Provide an explicit command: kx exec <index> -- /path/to/binary",
	)
}

// DebugCommand attaches an ephemeral debug container to an indexed pod.
//
// This is the answer to the dead end kx exec reaches on a distroless or
// scratch image: exec needs a shell inside the target, and those images have
// none, so it can only report that and stop. An ephemeral container brings its
// own shell into the running pod instead, without restarting it or changing
// what it runs.
//
// The container it adds stays on the pod's spec for as long as the pod lives —
// Kubernetes offers no way to remove one. That is kubectl's behaviour, not
// kx's, and the command's help says so rather than prompting: the change is
// additive, and a confirmation in front of every debugging session would be
// friction where kx delete's prompt guards actual loss.
type DebugCommand struct {
	Kubectl kubectl.Service
	State   IndexResolver
	// Image is the configured debug_image, used when the invocation names none.
	Image string
}

func (c DebugCommand) Execute(index int, command, extraArgs []string) error {
	name, namespace, kind, err := c.State.Fields(index)
	if err != nil {
		return err
	}
	if kind != kinds.Pod {
		return fmt.Errorf("debug is only supported for pods.")
	}

	args := []string{"debug", "-it", name, "-n", namespace}
	// An explicit --image is the user overriding their own default for one
	// run; passing the configured one as well would hand kubectl two.
	//
	// A malformed one is reported rather than dropped: with the error
	// discarded, `kx debug 1 --image` (value omitted, which a shell expanding
	// to nothing produces) read as "no image given", so kx appended its own
	// --image=busybox and forwarded the bare --image alongside it, leaving
	// kubectl to complain about a flag the user could see they had written.
	image, _, err := extractString(extraArgs, "--image", "")
	if err != nil {
		return err
	}
	if image == "" {
		args = append(args, "--image="+c.Image)
	}
	target, err := c.target(name, namespace, extraArgs)
	if err != nil {
		return err
	}
	if target != "" {
		args = append(args, "--target="+target)
	}
	args = append(args, extraArgs...)
	if len(command) > 0 {
		args = append(args, "--")
		args = append(args, command...)
	}

	code, err := c.Kubectl.RunInteractive(args, false)
	if err != nil {
		return err
	}
	if code != 0 {
		// kubectl already printed its own message; what is left is deciding
		// whether this was a stale index worth refreshing, and forwarding the
		// exit code either way — the same shape describe and exec use.
		return forwardExit(c.Kubectl, kinds.Pod, name, namespace, code)
	}
	return nil
}

// target names the container the debug container should share a process
// namespace with.
//
// Without it the debug container joins the pod but not the target's processes,
// so /proc/1/root — the target's filesystem, which is most of the reason to be
// here on an image with no shell — is out of reach. A pod with one container
// has only one answer, so kx supplies it; a pod with several is ambiguous and
// kubectl's own error names the candidates better than a guess would.
//
// Best-effort about the *lookup*: it costs one kubectl call, and a failure
// means the flag is omitted rather than the command refused. kubectl still
// runs, just without the process namespace shared. A malformed --target is a
// different thing and is reported, the same as a malformed --image.
func (c DebugCommand) target(name, namespace string, extraArgs []string) (string, error) {
	explicit, _, err := extractString(extraArgs, "--target", "")
	if err != nil {
		return "", err
	}
	if explicit != "" {
		return "", nil
	}
	output, err := c.Kubectl.Run([]string{
		"get", "pod", name, "-n", namespace,
		"-o", "jsonpath={range .spec.containers[*]}{.name}{\"\\n\"}{end}",
	})
	if err != nil {
		return "", nil
	}
	containers := strings.Fields(output)
	if len(containers) != 1 {
		return "", nil
	}
	return containers[0], nil
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
func (c ContextsCommand) Execute() (table index.Table, context string, err error) {
	output, err := c.Kubectl.Run([]string{"config", "get-contexts"})
	if err != nil {
		return index.Table{}, "", err
	}
	current := c.Kubectl.CurrentContext()
	// The CURRENT column is kept. It is blank on every row but the active one,
	// and that blank used to be destroyed between here and the screen: Add
	// prepended the index column, making the cell interior, and the renderer
	// re-parsed the padded text, where an empty cell and column padding are the
	// same run of spaces. Rows reach the renderer intact now, so the marker
	// kubectl prints is the marker kx prints.
	indexed := c.Index.Add(output)
	if len(indexed.Entries) > 0 {
		if err := c.State.SaveNamed(state.State{
			Resources: contextResources(indexed.Entries),
		}); err != nil {
			return index.Table{}, "", err
		}
	}
	return indexed, current, nil
}

// contextResources records the listed contexts, and nothing about namespaces.
//
// Not resourcesFrom: that carries each row's NAMESPACE cell onto the resource,
// which is right for `kx get -A` and wrong here. get-contexts prints a NAMESPACE
// column too, but it names the namespace a context *defaults to* rather than one
// the context lives in — contexts are kubeconfig entries and are not namespaced
// at all. Carried through, it made Resources.Spanning() true and captioned the
// slot "all namespaces".
//
// The entry namespace is left empty for the same reason. The active context is
// stamped onto State.Context by the state service, which is where every reader
// already looks for it; recording it as the scope as well captioned the listing
// with it twice.
func contextResources(entries []index.Entry) state.Resources {
	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		names = append(names, entry.Name)
	}
	return state.NewResources(names, kinds.Context)
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
