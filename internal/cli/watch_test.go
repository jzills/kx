package cli

import (
	"reflect"
	"testing"

	"github.com/jzills/kx/internal/index"
)

func watchShape(t *testing.T) index.TableShape {
	t.Helper()
	shape, ok := index.ParseHeader("EVENT      NAME             STATUS")
	if !ok {
		t.Fatal("ParseHeader: ok=false")
	}
	return shape
}

func watchAllNamespacesShape(t *testing.T) index.TableShape {
	t.Helper()
	shape, ok := index.ParseHeader("EVENT      NAMESPACE   NAME             STATUS")
	if !ok {
		t.Fatal("ParseHeader: ok=false")
	}
	return shape
}

// Captured live: a MODIFIED row's STATUS ("Terminating", 11 chars) is wider
// than the header assumed from an earlier ADDED row ("Running", 7 chars).
// watchRows must upsert the correctly-parsed row, not a garbled one, and a
// following DELETED must still remove it (proving the key it upserted under
// is the plain resource name, unaffected by the width drift).
func TestWatchRowsHandlesColumnWidthDriftAcrossEvents(t *testing.T) {
	shape, ok := index.ParseHeader("EVENT      NAME                        READY   STATUS    RESTARTS   AGE")
	if !ok {
		t.Fatal("ParseHeader: ok=false")
	}
	rows := newWatchRows()
	rows.Apply(shape, shape.Row("ADDED      waypoint-5d84f566ff-hb8rk   1/1     Running   0          106s"))
	rows.Apply(shape, shape.Row("MODIFIED   waypoint-5d84f566ff-hb8rk   1/1     Terminating   0          107s"))

	got := rows.Snapshot()
	want := [][]string{{"waypoint-5d84f566ff-hb8rk", "1/1", "Terminating", "0", "107s"}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Snapshot = %v, want %v", got, want)
	}

	rows.Apply(shape, shape.Row("DELETED    waypoint-5d84f566ff-hb8rk   1/1     Terminating   0          107s"))
	if got := rows.Snapshot(); len(got) != 0 {
		t.Errorf("Snapshot = %v, want empty after DELETED", got)
	}
}

// Names collide across namespaces, so -A rows must be keyed by
// NAMESPACE/NAME, not NAME alone — otherwise two unrelated pods with the
// same name in different namespaces would clobber each other's row.
func TestWatchRowsKeysByNamespaceAndNameForAllNamespaces(t *testing.T) {
	shape := watchAllNamespacesShape(t)
	rows := newWatchRows()
	rows.Apply(shape, shape.Row("ADDED      prod        worker-0         Running"))
	rows.Apply(shape, shape.Row("ADDED      staging     worker-0         Running"))

	got := rows.Snapshot()
	want := [][]string{{"prod", "worker-0", "Running"}, {"staging", "worker-0", "Running"}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Snapshot = %v, want %v — same-named pods in different namespaces must not collide", got, want)
	}
}

func TestWatchNamespaceAllNamespaces(t *testing.T) {
	if got := watchNamespace([]string{"-A"}, &fakeKubectl{}); got != "all namespaces" {
		t.Errorf("watchNamespace = %q, want %q", got, "all namespaces")
	}
}

func TestWatchNamespaceExplicit(t *testing.T) {
	if got := watchNamespace([]string{"-n", "staging"}, &fakeKubectl{}); got != "staging" {
		t.Errorf("watchNamespace = %q, want %q", got, "staging")
	}
}

func TestWatchNamespaceFallsBackToCurrentNamespace(t *testing.T) {
	kube := &fakeKubectl{namespace: "prod"}
	if got := watchNamespace(nil, kube); got != "prod" {
		t.Errorf("watchNamespace = %q, want %q", got, "prod")
	}
}

func TestWatchRowsDeletedOnlyRemovesMatchingNamespace(t *testing.T) {
	shape := watchAllNamespacesShape(t)
	rows := newWatchRows()
	rows.Apply(shape, shape.Row("ADDED      prod        worker-0         Running"))
	rows.Apply(shape, shape.Row("ADDED      staging     worker-0         Running"))
	rows.Apply(shape, shape.Row("DELETED    prod        worker-0         Terminating"))

	got := rows.Snapshot()
	want := [][]string{{"staging", "worker-0", "Running"}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Snapshot = %v, want %v — deleting prod/worker-0 must not remove staging/worker-0", got, want)
	}
}

func TestWatchRowsAddedUpsertsInOrder(t *testing.T) {
	shape := watchShape(t)
	rows := newWatchRows()
	rows.Apply(shape, shape.Row("ADDED      nginx            Running"))
	rows.Apply(shape, shape.Row("ADDED      redis            Running"))

	got := rows.Snapshot()
	want := [][]string{{"nginx", "Running"}, {"redis", "Running"}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Snapshot = %v, want %v", got, want)
	}
}

func TestWatchRowsModifiedUpdatesInPlace(t *testing.T) {
	shape := watchShape(t)
	rows := newWatchRows()
	rows.Apply(shape, shape.Row("ADDED      nginx            Pending"))
	rows.Apply(shape, shape.Row("ADDED      redis            Running"))
	rows.Apply(shape, shape.Row("MODIFIED   nginx            Running"))

	got := rows.Snapshot()
	want := [][]string{{"nginx", "Running"}, {"redis", "Running"}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Snapshot = %v, want %v (nginx keeps its position)", got, want)
	}
}

func TestWatchRowsDeletedRemoves(t *testing.T) {
	shape := watchShape(t)
	rows := newWatchRows()
	rows.Apply(shape, shape.Row("ADDED      nginx            Running"))
	rows.Apply(shape, shape.Row("ADDED      redis            Running"))
	rows.Apply(shape, shape.Row("DELETED    nginx            Terminating"))

	got := rows.Snapshot()
	want := [][]string{{"redis", "Running"}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Snapshot = %v, want %v", got, want)
	}
}

func TestWatchRowsReaddAfterDeleteAppendsAtEnd(t *testing.T) {
	shape := watchShape(t)
	rows := newWatchRows()
	rows.Apply(shape, shape.Row("ADDED      nginx            Running"))
	rows.Apply(shape, shape.Row("ADDED      redis            Running"))
	rows.Apply(shape, shape.Row("DELETED    nginx            Terminating"))
	rows.Apply(shape, shape.Row("ADDED      nginx            Pending"))

	got := rows.Snapshot()
	want := [][]string{{"redis", "Running"}, {"nginx", "Pending"}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Snapshot = %v, want %v", got, want)
	}
}

// Snapshot must return copies: render.RedrawTable's styling (alignRestarts)
// mutates row slices in place, and that must never corrupt what watchRows
// has stored, or every redraw would compound the previous one's padding.
func TestWatchRowsSnapshotReturnsIndependentCopies(t *testing.T) {
	shape := watchShape(t)
	rows := newWatchRows()
	rows.Apply(shape, shape.Row("ADDED      nginx            Running"))

	snap := rows.Snapshot()
	snap[0][1] = "MUTATED"

	again := rows.Snapshot()
	if again[0][1] != "Running" {
		t.Errorf("second Snapshot = %v, want the mutation not to have leaked into stored state", again)
	}
}
