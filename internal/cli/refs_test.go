package cli

import (
	"strings"
	"testing"

	"github.com/jzills/kx/internal/kinds"
	"github.com/jzills/kx/internal/state"
)

// resolveRefs is the one place a command's resource arguments are parsed and
// resolved, so ranges, dedupe and validation cannot differ between commands.
func TestResolveRefsParsesIndexesAndRanges(t *testing.T) {
	resolver := refOf(
		[3]string{"api", "prod", "Pod"},
		[3]string{"web", "prod", "Pod"},
		[3]string{"db", "prod", "Pod"},
	)

	resolved, err := resolveRefs(resolver, "indexes", []string{"1", "2..3"})
	if err != nil {
		t.Fatalf("resolveRefs: %v", err)
	}
	if len(resolved) != 3 {
		t.Fatalf("resolved %d references, want 3", len(resolved))
	}
	for i, want := range []string{"api", "web", "db"} {
		if resolved[i].Name != want {
			t.Errorf("resolved[%d].Name = %q, want %q", i, resolved[i].Name, want)
		}
		if resolved[i].Namespace != "prod" || resolved[i].Kind != kinds.Pod {
			t.Errorf("resolved[%d] = %+v, want prod/Pod", i, resolved[i])
		}
	}
}

// Deduped by what a reference resolved to, not by how it was written — which
// is the point of resolving first. Two spellings of one resource are one
// resource.
func TestResolveRefsDedupesByResolvedIdentity(t *testing.T) {
	resolver := refOf(
		[3]string{"api", "prod", "Pod"},
		[3]string{"api", "prod", "Pod"},
		[3]string{"web", "prod", "Pod"},
	)

	resolved, err := resolveRefs(resolver, "indexes", []string{"1", "2", "3"})
	if err != nil {
		t.Fatalf("resolveRefs: %v", err)
	}
	if len(resolved) != 2 {
		t.Fatalf("resolved %d references, want 2 — indexes 1 and 2 are one resource",
			len(resolved))
	}
	if resolved[0].Name != "api" || resolved[1].Name != "web" {
		t.Errorf("resolved = %+v, want api then web — first occurrence wins", resolved)
	}
}

// Every reference is resolved before the caller acts on any of them, so a bad
// one cannot leave a command half-done. This is what kx delete already
// guaranteed and kx cordon did not.
func TestResolveRefsRefusesTheWholeBatchOnOneBadReference(t *testing.T) {
	resolver := refOf([3]string{"api", "prod", "Pod"})

	if _, err := resolveRefs(resolver, "indexes", []string{"1", "99"}); err == nil {
		t.Fatal("resolveRefs accepted a batch containing an out-of-range index")
	}
}

// No arguments is the caller's arity error, reported in kx's voice.
func TestResolveRefsRefusesNoArguments(t *testing.T) {
	_, err := resolveRefs(refOf(), "indexes", nil)
	if err == nil {
		t.Fatal("resolveRefs accepted no arguments")
	}
	if !strings.Contains(err.Error(), "indexes") {
		t.Errorf("err = %q, want it to name the missing argument", err)
	}
}

// resolveRefsExpecting is resolveRefs for a caller that has already named the
// kind it wants — the kx get relist, so far. When the index's actual kind
// matches, it resolves exactly as resolveRefs would.
func TestResolveRefsExpectingResolvesWhenKindMatches(t *testing.T) {
	services := switchServices(t, nil)
	if err := services.State.Save(state.State{
		Resources: state.NewResources([]string{"api", "web"}, kinds.Pod), Namespace: "prod",
	}); err != nil {
		t.Fatalf("Save: %v", err)
	}

	resolved, err := resolveRefsExpecting(services.State, "indexes", []string{"1"}, kinds.Pod)
	if err != nil {
		t.Fatalf("resolveRefsExpecting: %v", err)
	}
	if len(resolved) != 1 {
		t.Fatalf("resolved %d references, want 1", len(resolved))
	}
	if resolved[0].Name != "api" || resolved[0].Namespace != "prod" || resolved[0].Kind != kinds.Pod {
		t.Errorf("resolved[0] = %+v, want api/prod/Pod", resolved[0])
	}
}

// An index that resolves to a kind other than the one the caller named is
// refused with the same message FieldsExpecting has always given — naming
// what the index actually is and what was expected, not a generic parse
// failure. This is what "run 'kx get pods' to relist" depends on: it comes
// from ResolveExpecting, not from anything resolveRefsExpecting adds itself.
func TestResolveRefsExpectingRefusesTheWrongKind(t *testing.T) {
	services := switchServices(t, nil)
	if err := services.State.Save(state.State{
		Resources: state.NewResources([]string{"web"}, kinds.Deployment), Namespace: "prod",
	}); err != nil {
		t.Fatalf("Save: %v", err)
	}

	_, err := resolveRefsExpecting(services.State, "indexes", []string{"1"}, kinds.Pod)
	if err == nil {
		t.Fatal("resolveRefsExpecting accepted an index that resolved to the wrong kind")
	}
	for _, want := range []string{"Deployment/web", "not Pod"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("err = %q\n  missing %q", err, want)
		}
	}
}

// resolveRefsExpecting shares resolveRefs's identity dedupe via
// resolveIndexes: two arguments that resolve to the same resource collapse
// to one, the same as resolveRefs.
func TestResolveRefsExpectingDedupesByResolvedIdentity(t *testing.T) {
	resolver := refOf(
		[3]string{"api", "prod", "Pod"},
		[3]string{"api", "prod", "Pod"},
	)

	resolved, err := resolveRefsExpecting(resolver, "indexes", []string{"1", "2"}, kinds.Pod)
	if err != nil {
		t.Fatalf("resolveRefsExpecting: %v", err)
	}
	if len(resolved) != 1 {
		t.Fatalf("resolved %d references, want 1 — indexes 1 and 2 are one resource",
			len(resolved))
	}
	if resolved[0].Name != "api" {
		t.Errorf("resolved = %+v, want api", resolved)
	}
}
