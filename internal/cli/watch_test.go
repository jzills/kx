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
