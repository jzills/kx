package cli

import (
	"context"
	"strings"
	"testing"

	"github.com/jzills/kx/internal/diagnostics"
	"github.com/jzills/kx/internal/kinds"
	"github.com/jzills/kx/internal/state"
)

// fakeGatherer replays a scripted sweep and records the namespace it was asked
// for — the empty string being how a cluster-wide sweep is spelled.
type fakeGatherer struct {
	sweep     []diagnostics.Data
	sweptWith string
}

func (f *fakeGatherer) Gather(
	context.Context, kinds.Kind, string, string,
) (diagnostics.Data, error) {
	return diagnostics.Data{}, nil
}

func (f *fakeGatherer) Sweep(_ context.Context, namespace string) ([]diagnostics.Data, error) {
	f.sweptWith = namespace
	return f.sweep, nil
}

// A desired-but-unready replica set is the cheapest Critical verdict to build.
func unhealthy(kind kinds.Kind, name, namespace string) diagnostics.Data {
	return diagnostics.Data{
		Kind: kind, Name: name, Namespace: namespace,
		Replicas: &diagnostics.ReplicaHealth{Desired: 2, Ready: 0, Available: 0, Updated: 2},
	}
}

func triageOf(gatherer Gatherer, saved *[]state.State) TriageCommand {
	return TriageCommand{
		Diagnostics: gatherer,
		Save: func(s state.State) error {
			*saved = append(*saved, s)
			return nil
		},
	}
}

// -A sweeps every namespace and saves nothing: names repeat across namespaces,
// so an index would resolve to whichever row happened to be written last.
func TestTriageAllNamespacesSweepsEverythingAndSavesNothing(t *testing.T) {
	gatherer := &fakeGatherer{sweep: []diagnostics.Data{
		unhealthy(kinds.Deployment, "web", "prod"),
	}}
	var saved []state.State

	result, err := triageOf(gatherer, &saved).
		Execute(context.Background(), "ignored", true)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if gatherer.sweptWith != "" {
		t.Errorf("swept %q, want every namespace", gatherer.sweptWith)
	}
	if !result.AllNamespaces {
		t.Error("result is not marked all-namespaces, so it would render an X column")
	}
	if len(saved) != 0 {
		t.Errorf("saved %d states, want none", len(saved))
	}
}

func TestTriageScopedSweepSavesStateForItsNamespace(t *testing.T) {
	gatherer := &fakeGatherer{sweep: []diagnostics.Data{
		unhealthy(kinds.Deployment, "web", "prod"),
	}}
	var saved []state.State

	result, err := triageOf(gatherer, &saved).
		Execute(context.Background(), "prod", false)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if gatherer.sweptWith != "prod" {
		t.Errorf("swept %q, want prod", gatherer.sweptWith)
	}
	if result.AllNamespaces {
		t.Error("a scoped sweep is marked all-namespaces")
	}
	if len(saved) != 1 || saved[0].Namespace != "prod" {
		t.Fatalf("saved = %+v, want one state for prod", saved)
	}
}

// The collision drop exists only to keep an index from resolving to the wrong
// resource. A cluster-wide sweep has no indexes, so a name shared across two
// namespaces must show both rows rather than silently losing one.
func TestTriageAllNamespacesKeepsNamesakesFromDifferentNamespaces(t *testing.T) {
	gatherer := &fakeGatherer{sweep: []diagnostics.Data{
		unhealthy(kinds.Deployment, "web", "prod"),
		unhealthy(kinds.Deployment, "web", "staging"),
	}}
	var saved []state.State

	result, err := triageOf(gatherer, &saved).Execute(context.Background(), "", true)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if len(result.Reports) != 2 {
		t.Errorf("reported %d rows, want both namespaces' web", len(result.Reports))
	}
	if len(result.Dropped) != 0 {
		t.Errorf("dropped %v, want nothing dropped when no state is saved", result.Dropped)
	}
}

// Within one namespace the drop still applies: state is keyed by name alone, so
// a Deployment and a Service both called web cannot both be indexed.
func TestTriageScopedSweepStillDropsCrossKindNamesakes(t *testing.T) {
	gatherer := &fakeGatherer{sweep: []diagnostics.Data{
		unhealthy(kinds.Deployment, "web", "prod"),
		unhealthy(kinds.StatefulSet, "web", "prod"),
	}}
	var saved []state.State

	result, err := triageOf(gatherer, &saved).Execute(context.Background(), "prod", false)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if len(result.Reports) != 1 {
		t.Errorf("reported %d rows, want the collision dropped", len(result.Reports))
	}
	if len(result.Dropped) != 1 || !strings.Contains(result.Dropped[0], "web") {
		t.Errorf("dropped = %v, want the StatefulSet named", result.Dropped)
	}
}

// Both guards return before any cluster call, which is what lets them run
// against an empty Services{} — a premature call would nil-panic instead.
func TestDiagRejectsAScopeFlagAlongsideAnIndex(t *testing.T) {
	for _, flag := range []struct{ name, value string }{
		{"namespace", "prod"},
		{"all-namespaces", "true"},
	} {
		quietRender(t)
		cmd := newDiagnosticCommand(Services{}, "diagnostic", nil)
		if err := cmd.Flags().Set(flag.name, flag.value); err != nil {
			t.Fatalf("set --%s: %v", flag.name, err)
		}
		err := cmd.RunE(cmd, []string{"1"})
		if err == nil {
			t.Fatalf("--%s was accepted alongside an index", flag.name)
		}
		if !strings.Contains(err.Error(), "cannot be combined with an index") {
			t.Errorf("--%s: err = %v", flag.name, err)
		}
	}
}

func TestDiagRejectsNamespaceAndAllNamespacesTogether(t *testing.T) {
	quietRender(t)
	cmd := newDiagnosticCommand(Services{}, "diagnostic", nil)
	if err := cmd.Flags().Set("namespace", "prod"); err != nil {
		t.Fatalf("set --namespace: %v", err)
	}
	if err := cmd.Flags().Set("all-namespaces", "true"); err != nil {
		t.Fatalf("set --all-namespaces: %v", err)
	}
	if err := cmd.RunE(cmd, nil); err == nil {
		t.Fatal("-n and -A were accepted together")
	} else if !strings.Contains(err.Error(), "cannot be combined") {
		t.Errorf("err = %v", err)
	}
}

func TestDiagnosticRegistersHTMLFlags(t *testing.T) {
	cmd := newDiagnosticCommand(Services{}, "diagnostic", []string{"diag"})
	for _, name := range []string{"html", "port", "no-open"} {
		if cmd.Flags().Lookup(name) == nil {
			t.Errorf("--%s is not registered, so it will not appear in --help", name)
		}
	}
}
