package cli

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/spf13/cobra"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/fields"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/client-go/kubernetes"

	"github.com/jzills/kx/internal/kinds"
	"github.com/jzills/kx/internal/kubectl"
	"github.com/jzills/kx/internal/render"
)

// defaultWaitTimeout is kubectl wait's own default, applied to the Job watch
// too, so one rule holds however a wait is carried out.
const defaultWaitTimeout = 30 * time.Second

// waitDefaults are the conditions kx waits for when no --for is given, for
// the kinds where one condition means "ready" unambiguously. Label is what
// the success line says was met.
//
// Jobs and Services are absent because kx waits for them itself — see
// WaitCommand.waitJob and waitService. Deployments, StatefulSets and
// DaemonSets are absent on purpose: Available is true in the middle of a
// rollout, so it would report done while old pods are still being replaced.
// kx rollout status is the wait for those.
var waitDefaults = map[kinds.Kind]struct{ For, Label string }{
	kinds.Pod:                   {"condition=Ready", "Ready"},
	kinds.Node:                  {"condition=Ready", "Ready"},
	kinds.PersistentVolumeClaim: {"jsonpath={.status.phase}=Bound", "Bound"},
}

// WaitCommand blocks until each resolved resource reaches its condition.
type WaitCommand struct {
	Kubectl    kubectl.Service
	Kubernetes func() (kubernetes.Interface, error)
	Status     func(string) func()
}

// Execute waits for one resource and returns what was met, for the success
// line. extraArgs are kubectl's flags; a --for among them replaces the kind's
// default, and is passed to kubectl wait whatever the kind.
func (c WaitCommand) Execute(
	ctx context.Context, target Resolved, timeout time.Duration, extraArgs []string,
) (string, error) {
	subject := string(target.Kind) + "/" + target.Name
	stop := c.Status("waiting for " + subject)
	defer stop()

	if hasFlag(extraArgs, "--for", "") {
		return c.kubectlWait(target, nil, extraArgs, forLabel(extraArgs))
	}
	switch {
	case target.Kind == kinds.Job:
		return c.waitJob(ctx, target, timeout)
	case target.Kind == kinds.Service:
		return c.waitService(ctx, target, extraArgs)
	case rolloutKinds.Has(target.Kind):
		return "", fmt.Errorf(
			"A %s is waited for with 'kx rollout status %s' — its Available condition "+
				"is true mid-rollout, so it can't say the rollout finished. "+
				"Pass --for to wait on a condition anyway.",
			target.Kind, target.Ref)
	}
	condition, ok := waitDefaults[target.Kind]
	if !ok {
		return "", fmt.Errorf(
			"kx has no default condition for a %s — pass one with --for, "+
				"e.g. --for=condition=Ready, or --for=delete.", target.Kind)
	}
	return c.kubectlWait(target, []string{"--for=" + condition.For}, extraArgs, condition.Label)
}

// kubectlWait runs kubectl wait and reports label once it returns. kubectl's
// own "condition met" line is replaced by kx's, so it is captured, not
// streamed; kubectl prints nothing worth seeing while it waits.
func (c WaitCommand) kubectlWait(target Resolved, forArgs, extraArgs []string, label string) (string, error) {
	args := []string{"wait", string(target.Kind) + "/" + target.Name}
	if target.Namespace != "" {
		args = append(args, "-n", target.Namespace)
	}
	args = append(append(args, forArgs...), extraArgs...)
	if _, err := c.Kubectl.Run(args); err != nil {
		return "", err
	}
	return label, nil
}

// forLabel names what a caller's own --for waited on: "deleted" for delete,
// the value itself otherwise, and every value joined when there are several
// (kubectl requires all of them).
func forLabel(extraArgs []string) string {
	var values []string
	rest := extraArgs
	for hasFlag(rest, "--for", "") {
		value, remaining, err := extractString(rest, "--for", "")
		if err != nil {
			break
		}
		rest = remaining
		if value == "delete" {
			value = "deleted"
		}
		values = append(values, value)
	}
	return strings.Join(values, ", ")
}

// waitJob watches a Job until it completes or fails.
//
// Through the API rather than kubectl wait, because kubectl cannot wait for
// "Complete or Failed": repeated --for flags must all hold, so waiting on
// Complete alone hangs a failed Job until the timeout. Read first, then
// watched from that read's version, so a Job that finished before the wait
// began returns at once and nothing between the two is missed.
func (c WaitCommand) waitJob(ctx context.Context, target Resolved, timeout time.Duration) (string, error) {
	client, err := c.Kubernetes()
	if err != nil {
		return "", err
	}
	jobs := client.BatchV1().Jobs(target.Namespace)
	// kubectl's --timeout=0: read once, and a Job not finished by then has
	// timed out. Before the deadline is set, since a zero one has already
	// passed, and a read made under it fails without being sent.
	if timeout == 0 {
		job, err := jobs.Get(ctx, target.Name, metav1.GetOptions{})
		if err != nil {
			return "", c.apiError(ctx, target, timeout, err)
		}
		if label, err, done := jobOutcome(target, job); done {
			return label, err
		}
		return "", timedOut(target, timeout)
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	for {
		job, err := jobs.Get(ctx, target.Name, metav1.GetOptions{})
		if err != nil {
			return "", c.apiError(ctx, target, timeout, err)
		}
		if label, err, done := jobOutcome(target, job); done {
			return label, err
		}
		watcher, err := jobs.Watch(ctx, metav1.ListOptions{
			FieldSelector:   fields.OneTermEqualSelector("metadata.name", target.Name).String(),
			ResourceVersion: job.ResourceVersion,
		})
		if err != nil {
			return "", c.apiError(ctx, target, timeout, err)
		}
		label, err, done := watchJob(ctx, target, watcher)
		watcher.Stop()
		if done {
			return label, err
		}
		// The channel closes on a server-side watch timeout as well as on
		// ours; only ours ends the wait. Otherwise read and watch again.
		if ctx.Err() != nil {
			return "", timedOut(target, timeout)
		}
	}
}

// jobOutcome reads a Job's terminal condition, if it has one yet.
func jobOutcome(target Resolved, job *batchv1.Job) (label string, err error, done bool) {
	for _, condition := range job.Status.Conditions {
		if condition.Status != corev1.ConditionTrue {
			continue
		}
		switch condition.Type {
		case batchv1.JobComplete:
			return "Complete", nil, true
		case batchv1.JobFailed:
			reason := condition.Reason
			if condition.Message != "" {
				reason += " — " + condition.Message
			}
			return "", fmt.Errorf("%s/%s failed: %s", target.Kind, target.Name, reason), true
		}
	}
	return "", nil, false
}

// waitService waits for a LoadBalancer Service's address. Any other type
// never gets one, so waiting would only ever time out; that is refused
// instead, naming the type.
func (c WaitCommand) waitService(ctx context.Context, target Resolved, extraArgs []string) (string, error) {
	client, err := c.Kubernetes()
	if err != nil {
		return "", err
	}
	service, err := client.CoreV1().Services(target.Namespace).Get(ctx, target.Name, metav1.GetOptions{})
	if err != nil {
		return "", c.apiError(ctx, target, 0, err)
	}
	if service.Spec.Type != corev1.ServiceTypeLoadBalancer {
		return "", fmt.Errorf(
			"%s/%s is a %s Service, which is never given an external address — "+
				"only a LoadBalancer is. Pass --for to wait on something else.",
			target.Kind, target.Name, service.Spec.Type)
	}
	return c.kubectlWait(target,
		[]string{"--for=jsonpath={.status.loadBalancer.ingress}"}, extraArgs, "has an address")
}

// apiError turns a failed API read into the error kx reports: a vanished
// resource into the stale-index error withRefresh relists on, our own
// deadline into the timeout message, anything else as it came.
func (c WaitCommand) apiError(ctx context.Context, target Resolved, timeout time.Duration, err error) error {
	switch {
	case apierrors.IsNotFound(err):
		return StaleResourceError{
			Kind: target.Kind, Name: target.Name, Namespace: target.Namespace, Ref: target.Ref,
		}
	case ctx.Err() != nil && errors.Is(err, ctx.Err()):
		return timedOut(target, timeout)
	}
	return err
}

func timedOut(target Resolved, timeout time.Duration) error {
	return fmt.Errorf("Timed out after %s waiting for %s/%s.", timeout, target.Kind, target.Name)
}

// negativeWaitTimeout is what kubectl waits for given a negative --timeout.
const negativeWaitTimeout = 7 * 24 * time.Hour

// waitTimeout reads --timeout for the waits kx carries out itself, leaving it
// in the arguments for the ones kubectl does. kubectl's spelling and meaning:
// a Go duration, where 0 means check once and a negative one means a week.
func waitTimeout(extraArgs []string) (time.Duration, error) {
	value, _, err := extractString(extraArgs, "--timeout", "")
	if err != nil || value == "" {
		return defaultWaitTimeout, err
	}
	timeout, err := time.ParseDuration(value)
	if err != nil {
		return 0, fmt.Errorf("Invalid value for '--timeout': '%s' is not a duration like 30s or 5m.", value)
	}
	if timeout < 0 {
		return negativeWaitTimeout, nil
	}
	return timeout, nil
}

func newWaitCommand(services Services) *cobra.Command {
	return &cobra.Command{
		Use:        "wait <index>... [kubectl flags]",
		SuggestFor: []string{"until", "block"},
		Short:      "Wait until indexed resources are ready: a Pod or Node Ready, a PVC Bound, a Job Complete, a LoadBalancer Service given an address.",
		Long: "Blocks until each indexed resource reaches its condition, then prints it. " +
			"With no --for, the condition comes from the kind: a Pod or Node is Ready, a " +
			"PersistentVolumeClaim Bound, a Job Complete — and a Job that fails ends the " +
			"wait at once, with its reason, rather than running to the timeout — and a " +
			"LoadBalancer Service has an address.\n\n" +
			"A Deployment, StatefulSet or DaemonSet is waited for with kx rollout status, " +
			"which knows when a rollout has finished; any other kind needs --for.\n\n" +
			"--for replaces the default and goes to kubectl wait as written: " +
			"--for=condition=Ready, --for=delete, --for=jsonpath=... . --timeout is 30s " +
			"unless given, as it is for kubectl.\n\n" +
			"Several indexes are all resolved before anything waits, then waited for in " +
			"order; the first that fails or times out ends the command.\n\n" +
			"Unrecognized flags are passed through to kubectl.",
		Example: "  kx wait 2\n  kx wait 1..4 --timeout=2m\n  kx wait @db-claim\n" +
			"  kx wait 3 --for=delete\n  kx wait 5 --for=condition=Ready=false",
		// No Args validator, for the reason scale has none: cobra counts the
		// forwarded kubectl flags as arguments. The arity check is below.
		DisableFlagParsing: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			rest, handled, err := passthrough(cmd, args, nil)
			if err != nil || handled {
				return err
			}
			indexArgs, extra := splitLeadingIndexes(rest)
			if len(indexArgs) == 0 {
				return requiredArgsError(cmd)
			}
			timeout, err := waitTimeout(extra)
			if err != nil {
				return err
			}
			// Every reference resolved before any wait begins: a typo in the
			// last one should cost nothing, not minutes of waiting first.
			resolved, err := resolveRefs(services.State, "indexes", indexArgs)
			if err != nil {
				return err
			}
			if err := refuseScopeFlagResolved(resolved, extra); err != nil {
				return err
			}
			command := WaitCommand{
				Kubectl: services.Kubectl, Kubernetes: services.Kubernetes, Status: render.Status,
			}
			for _, target := range resolved {
				met, err := command.Execute(cmd.Context(), target, timeout, extra)
				if err != nil {
					return err
				}
				render.Success(strings.Join(captionPartsOf(
					string(target.Kind)+"/"+target.Name, target.Namespace, met), " · "))
			}
			return nil
		},
	}
}

// captionPartsOf keeps the non-empty parts in order, so a cluster-scoped
// resource's line has no empty namespace segment.
func captionPartsOf(parts ...string) []string {
	kept := make([]string, 0, len(parts))
	for _, part := range parts {
		if part != "" {
			kept = append(kept, part)
		}
	}
	return kept
}

// watchJob reads events until the Job finishes, the channel closes, or ctx
// ends — the deadline is selected on directly rather than left to close the
// channel, which a watch is not obliged to do promptly.
func watchJob(ctx context.Context, target Resolved, watcher watch.Interface) (string, error, bool) {
	for {
		select {
		case <-ctx.Done():
			return "", nil, false
		case event, open := <-watcher.ResultChan():
			if !open {
				return "", nil, false
			}
			if updated, ok := event.Object.(*batchv1.Job); ok {
				if label, err, done := jobOutcome(target, updated); done {
					return label, err, true
				}
			}
		}
	}
}
