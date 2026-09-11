package cli

import (
	"bytes"
	"context"
	"strings"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/fake"

	"github.com/jzills/kx/internal/config"
	"github.com/jzills/kx/internal/events"
	"github.com/jzills/kx/internal/index"
	"github.com/jzills/kx/internal/kinds"
	"github.com/jzills/kx/internal/state"
)

// podEvent is one event against Pod prod/nginx, last seen at the given moment.
func podEvent(reason string, at time.Time) *corev1.Event {
	return &corev1.Event{
		ObjectMeta:     metav1.ObjectMeta{Name: "nginx." + reason, Namespace: "prod"},
		InvolvedObject: corev1.ObjectReference{Name: "nginx", Kind: "Pod"},
		Reason:         reason,
		Type:           "Warning",
		Message:        reason + " happened",
		LastTimestamp:  metav1.NewTime(at),
	}
}

// eventsServices wires kx events against a fake API server holding the given
// events, and a saved listing whose index 1 is prod/nginx. The returned buffer
// is what kx rendered.
//
// The capture is taken after switchServices, not before: switchServices
// reconfigures the package renderer, so a capture set up first is silently
// thrown away — and every assertion of the form "the output does not contain
// X" then passes on an empty string.
func eventsServices(
	t *testing.T, cfg config.Config, held ...*corev1.Event,
) (Services, *recordingKubectl, *bytes.Buffer) {
	t.Helper()
	kube := &recordingKubectl{}
	services := switchServices(t, kube)
	sink := captureRender(t)
	services.Config = cfg
	services.Index = index.Service{}
	objects := make([]runtime.Object, 0, len(held))
	for _, event := range held {
		objects = append(objects, event)
	}
	client := fake.NewSimpleClientset(objects...)
	services.Kubernetes = func() (kubernetes.Interface, error) { return client, nil }
	if err := services.State.Save(state.State{
		Resources: state.NewResources([]string{"nginx"}, kinds.Pod),
		Namespace: "prod",
	}); err != nil {
		t.Fatalf("Save: %v", err)
	}
	return services, kube, sink
}

// The window is applied to the listing, not to the lookup: kx diag already
// bounds the warning events behind a verdict, and kx events showing the same
// object's events unbounded made one command's answer depend on which command
// was asked.
func TestEventsWindowDropsWhatHappenedBeforeIt(t *testing.T) {
	now := time.Now()
	services, _, sink := eventsServices(t, config.Default(),
		podEvent("Recent", now.Add(-10*time.Minute)),
		podEvent("Ancient", now.Add(-30*24*time.Hour)))

	if err := Execute(NewRoot(services, "test"), []string{"events", "1", "--since", "1h"}); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	out := sink.String()
	if !strings.Contains(out, "Recent") {
		t.Errorf("output = %q, want the event inside the window", out)
	}
	if strings.Contains(out, "Ancient") {
		t.Errorf("output = %q, want the event outside the window dropped", out)
	}
}

// The caption says what the listing was allowed to see, in the vocabulary
// --since reads, so an empty-looking listing can be told from a narrow one.
func TestEventsCaptionNamesTheWindow(t *testing.T) {
	services, _, sink := eventsServices(t, config.Default(), podEvent("Recent", time.Now()))

	if err := Execute(NewRoot(services, "test"), []string{"events", "1", "--since", "30m"}); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if out := sink.String(); !strings.Contains(out, "last 30m") {
		t.Errorf("output = %q, want the caption to name the window", out)
	}
}

// An unbounded run says nothing about a window, because there is none to name.
func TestEventsCaptionSaysNothingWithoutAWindow(t *testing.T) {
	services, _, sink := eventsServices(t, config.Default(), podEvent("Recent", time.Now()))

	if err := Execute(NewRoot(services, "test"), []string{"events", "1"}); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if out := sink.String(); strings.Contains(out, "last ") {
		t.Errorf("output = %q, want no window named on an unbounded run", out)
	}
}

// "No events found" means two different things once a window exists, and only
// one of them is reassuring.
func TestEventsEmptyListingQualifiesItselfWithTheWindow(t *testing.T) {
	services, _, sink := eventsServices(t, config.Default(),
		podEvent("Ancient", time.Now().Add(-30*24*time.Hour)))

	if err := Execute(NewRoot(services, "test"), []string{"events", "1", "--since", "1h"}); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	out := sink.String()
	if !strings.Contains(out, "No events found in the last 1h") {
		t.Errorf("output = %q, want the empty listing to name the window", out)
	}
}

// The staleness check asks whether the resource still exists, and an empty
// listing is the only hint kx gets. A window emptying the listing is not that
// hint: the object plainly exists, it just did nothing lately — probing for it
// would spend a kubectl subprocess to learn what the events already said.
func TestEventsDoesNotProbeWhenOnlyTheWindowEmptiedTheListing(t *testing.T) {
	services, kube, _ := eventsServices(t, config.Default(),
		podEvent("Ancient", time.Now().Add(-30*24*time.Hour)))

	if err := Execute(NewRoot(services, "test"), []string{"events", "1", "--since", "1h"}); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if len(kube.probes) != 0 {
		t.Errorf("kubectl probed %v, want no staleness check for a windowed-out listing",
			kube.probes)
	}
}

// ...while an object with no events at all still gets one, which is how kx
// tells "nothing happened" from "this resource is gone".
func TestEventsStillProbesWhenTheObjectHasNoEventsAtAll(t *testing.T) {
	services, kube, _ := eventsServices(t, config.Default())

	if err := Execute(NewRoot(services, "test"), []string{"events", "1", "--since", "1h"}); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if len(kube.probes) != 1 {
		t.Errorf("kubectl probed %d times, want the staleness check to run", len(kube.probes))
	}
}

// The flag falls back to events_max_age, and to nothing else: diag's window is
// a separate setting for a separate question.
func TestEventsWindowReadsItsOwnSetting(t *testing.T) {
	cfg := config.Default()
	cfg.EventsMaxAge = 90 * time.Minute
	cfg.DiagMaxAge = 7 * 24 * time.Hour

	got, err := resolveWindow("", cfg.EventsMaxAge)
	if err != nil {
		t.Fatalf("resolveWindow: %v", err)
	}
	if got != 90*time.Minute {
		t.Errorf("window = %v, want the configured events_max_age", got)
	}
}

func TestEventsRegistersSinceFlag(t *testing.T) {
	if newEventsCommand(Services{}).Flags().Lookup("since") == nil {
		t.Error("--since is not registered, so it will not appear in --help")
	}
}

// The flag's help names the setting it falls back to and what that setting
// currently holds — the defect #347 fixed for kx diag, not repeated here.
func TestEventsSinceHelpNamesTheConfiguredDefault(t *testing.T) {
	cfg := config.Default()
	cfg.EventsMaxAge = 36 * time.Hour
	usage := newEventsCommand(Services{Config: cfg}).Flags().Lookup("since").Usage
	if !strings.Contains(usage, "events_max_age") {
		t.Errorf("--since help = %q, want it to name events_max_age", usage)
	}
	if !strings.Contains(usage, "36h") {
		t.Errorf("--since help = %q, want it to name the configured 36h", usage)
	}
	if strings.Contains(usage, "unset") {
		t.Errorf("--since help = %q, want it not to call a set events_max_age unset", usage)
	}
}

// A malformed window is refused before the API server is read, and the error
// names the flag the way kx diag's does.
func TestEventsRejectsAMalformedSince(t *testing.T) {
	services, _, _ := eventsServices(t, config.Default(), podEvent("Recent", time.Now()))

	err := Execute(NewRoot(services, "test"), []string{"events", "1", "--since", "7 weeks"})
	if err == nil {
		t.Fatal("kx events accepted '7 weeks'")
	}
	if !strings.Contains(err.Error(), "--since") {
		t.Errorf("err = %v, want it to name --since", err)
	}
}

// Execute is the unit under the command: a listing with no window still hands
// back every event the object has.
func TestEventsCommandWithoutAWindowKeepsEverything(t *testing.T) {
	now := time.Now()
	client := fake.NewSimpleClientset(
		podEvent("Recent", now.Add(-10*time.Minute)),
		podEvent("Ancient", now.Add(-30*24*time.Hour)))
	command := EventsCommand{
		Kubectl: &recordingKubectl{},
		State:   pod("nginx"),
		Events:  events.APIService{Client: client},
	}
	rows, err := command.Execute(context.Background(), 1)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if len(rows) != 2 {
		t.Errorf("got %d rows, want both events", len(rows))
	}
}
