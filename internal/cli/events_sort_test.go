package cli

import (
	"slices"
	"strings"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/client-go/kubernetes"

	"github.com/jzills/kx/internal/config"
)

// normalPodEvent is podEvent of type Normal.
func normalPodEvent(reason string, at time.Time) *corev1.Event {
	event := podEvent(reason, at)
	event.Type = "Normal"
	return event
}

// reasonOrder is the order the given reasons appear in a rendered table.
func reasonOrder(t *testing.T, out string, reasons ...string) string {
	t.Helper()
	ordered := slices.Clone(reasons)
	for _, reason := range reasons {
		if !strings.Contains(out, reason+" ") {
			t.Fatalf("output has no %s row:\n%s", reason, out)
		}
	}
	slices.SortFunc(ordered, func(a, b string) int {
		return strings.Index(out, a+" ") - strings.Index(out, b+" ")
	})
	return strings.Join(ordered, ",")
}

// kx events --sort orders each table by the columns it prints. Every case
// runs the same four events, so a sort that did nothing would print them in
// the fake API server's one order and fail all but at most one case.
func TestEventsSortOrdersTheTableByItsColumns(t *testing.T) {
	now := time.Now()
	held := []*corev1.Event{
		normalPodEvent("Pulled", now.Add(-10*time.Minute)),
		podEvent("BackOff", now.Add(-1*time.Minute)),
		podEvent("Failed", now.Add(-30*time.Minute)),
		normalPodEvent("Created", now.Add(-5*time.Minute)),
	}
	for _, tc := range []struct {
		sort, want string
	}{
		{"age", "BackOff,Created,Pulled,Failed"},
		{"-age", "Failed,Pulled,Created,BackOff"},
		{"-type,age", "BackOff,Failed,Created,Pulled"},
		{"reason", "BackOff,Created,Failed,Pulled"},
	} {
		services, _, sink := eventsServices(t, config.Default(), held...)
		if err := Execute(NewRoot(services, "test"), []string{"events", "1", "--sort", tc.sort}); err != nil {
			t.Fatalf("--sort %s: %v", tc.sort, err)
		}
		if got := reasonOrder(t, sink.String(), "Pulled", "BackOff", "Failed", "Created"); got != tc.want {
			t.Errorf("--sort %s: rows %s, want %s", tc.sort, got, tc.want)
		}
	}
}

// AGE sorts by the event's own moment, not the age the table prints: two
// events a minute and two minutes old less a second both print as 1m, and
// sorted by that text they tie and keep the cluster's order — which is right
// for one direction and wrong for the other.
func TestEventsSortByAgeIsFinerThanTheAgeShown(t *testing.T) {
	now := time.Now()
	held := []*corev1.Event{
		podEvent("Older", now.Add(-119*time.Second)),
		podEvent("Newer", now.Add(-61*time.Second)),
	}
	for _, tc := range []struct {
		sort, want string
	}{
		{"age", "Newer,Older"},
		{"-age", "Older,Newer"},
	} {
		services, _, sink := eventsServices(t, config.Default(), held...)
		if err := Execute(NewRoot(services, "test"), []string{"events", "1", "--sort", tc.sort}); err != nil {
			t.Fatalf("--sort %s: %v", tc.sort, err)
		}
		if got := reasonOrder(t, sink.String(), "Older", "Newer"); got != tc.want {
			t.Errorf("--sort %s: rows %s, want %s", tc.sort, got, tc.want)
		}
	}
}

// A --sort naming no column kx events prints, or no column at all, is
// refused in the sentences every --sort uses, before the API server is read.
func TestEventsSortRefusesAColumnTheTableHasNone(t *testing.T) {
	for _, tc := range []struct {
		argv []string
		want string
	}{
		{
			[]string{"events", "1", "--sort", "count"},
			"Invalid value for '--sort': 'count'. Accepted values: type, reason, kind, age, message.",
		},
		{
			[]string{"events", "1", "--sort", ""},
			"Invalid value for '--sort': '' names no column — name each column, separated by commas: 'status,-restarts'.",
		},
	} {
		services, _, _ := eventsServices(t, config.Default(), podEvent("Recent", time.Now()))
		read := false
		client, err := services.Kubernetes()
		if err != nil {
			t.Fatal(err)
		}
		services.Kubernetes = func() (kubernetes.Interface, error) { read = true; return client, nil }
		err = Execute(NewRoot(services, "test"), tc.argv)
		if err == nil || err.Error() != tc.want {
			t.Errorf("%v: err = %v, want %q", tc.argv, err, tc.want)
		}
		if read {
			t.Errorf("%v: the API server was read before the refusal", tc.argv)
		}
	}
}
