package cli

import (
	"strings"
	"testing"

	"github.com/jzills/kx/internal/kinds"
	"github.com/jzills/kx/internal/state"
)

// A '@'-prefixed argument becomes a mark reference; anything else parses as
// an index exactly as before.
func TestParseRefsReadsTheSigil(t *testing.T) {
	resolver := refOf([3]string{"api", "prod", "Pod"}, [3]string{"web", "prod", "Pod"})

	refs, err := parseRefs(resolver, "indexes", []string{"@api", "2"})
	if err != nil {
		t.Fatalf("parseRefs: %v", err)
	}
	if len(refs) != 2 {
		t.Fatalf("parseRefs = %v, want two references", refs)
	}
	if refs[0].Mark != "api" || refs[0].Index != 0 {
		t.Errorf("refs[0] = %+v, want the mark api", refs[0])
	}
	if refs[1].Index != 2 || refs[1].Mark != "" {
		t.Errorf("refs[1] = %+v, want index 2", refs[1])
	}
}

// A mark names one resource; a range is positional by definition. Refused at
// either end and in both spellings.
func TestParseRefsRefusesARangeContainingAMark(t *testing.T) {
	resolver := refOf([3]string{"api", "prod", "Pod"})

	for _, arg := range []string{"@a..@b", "@api..5", "5..@api"} {
		_, err := parseRefs(resolver, "indexes", []string{arg})
		if err == nil {
			t.Errorf("parseRefs(%q) succeeded, want a refusal", arg)
			continue
		}
		if !strings.Contains(err.Error(), "range") {
			t.Errorf("parseRefs(%q) error = %q, want it to explain the range", arg, err)
		}
	}
}

// A purely numeric mark name would make @3 ambiguous against index 3.
func TestValidMarkNameRefusesNumbersAndJunk(t *testing.T) {
	for _, name := range []string{"3", "42", "", "a b", "api/web", "@api"} {
		if err := validMarkName(name); err == nil {
			t.Errorf("validMarkName(%q) = nil, want a refusal", name)
		}
	}
	for _, name := range []string{"api", "web-1", "db_primary", "api.v2", "a3"} {
		if err := validMarkName(name); err != nil {
			t.Errorf("validMarkName(%q) = %v, want it accepted", name, err)
		}
	}
}

// A Ref must never carry both an Index and a Mark — Ref's own doc says the
// two fields are mutually exclusive, and Resolve silently prefers the mark
// when both are set, so a Ref built with both would resolve to a different
// resource than its Index suggests. parseRef and parseRefs are the only two
// places a Ref is built from argv, so this checks both directly rather than
// trusting that neither branch above ever sets the other field: a future
// edit that, say, defaulted Index to the mark's string length would pass
// every other test here and still violate this.
func TestParseRefAndParseRefsNeverSetBothIndexAndMark(t *testing.T) {
	resolver := refOf([3]string{"api", "prod", "Pod"}, [3]string{"web", "prod", "Pod"})

	check := func(t *testing.T, ref state.Ref) {
		t.Helper()
		if ref.Index != 0 && ref.Mark != "" {
			t.Errorf("ref = %+v, carries both an Index and a Mark", ref)
		}
	}

	markRef, err := parseRef("index", "@api")
	if err != nil {
		t.Fatalf("parseRef(@api): %v", err)
	}
	check(t, markRef)
	if markRef.Mark != "api" {
		t.Errorf("parseRef(@api) = %+v, want the mark api", markRef)
	}

	indexRef, err := parseRef("index", "2")
	if err != nil {
		t.Fatalf("parseRef(2): %v", err)
	}
	check(t, indexRef)
	if indexRef.Index != 2 {
		t.Errorf("parseRef(2) = %+v, want index 2", indexRef)
	}

	refs, err := parseRefs(resolver, "indexes", []string{"@api", "2"})
	if err != nil {
		t.Fatalf("parseRefs: %v", err)
	}
	for _, ref := range refs {
		check(t, ref)
	}
}

// The threading a command relies on end to end: "@api" on argv reaches a
// DisableFlagParsing command whose leading-argument split
// (splitLeadingIndexes) has to recognize the sigil, whose resolveRefs has to
// parse it into a mark Ref rather than rebuilding one from an int, and whose
// Execute has to pass that Ref to Resolve unchanged. kx logs is exercised
// through the real cobra command and a real state.Service (not a hand-built
// Ref or a resolver that ignores its argument) so a regression in any one
// link — the leading-run split, the parser, or the dedupe/rebuild in
// resolveIndexes — fails this rather than being masked by the others.
func TestLogsResolvesAMarkGivenOnTheCommandLine(t *testing.T) {
	kube := &recordingKubectl{}
	services := switchServices(t, kube)
	if err := services.State.SaveMark("api", state.Mark{
		Resource: state.Resource{Name: "api-7d8f", Kind: kinds.Pod, Namespace: "prod"},
	}); err != nil {
		t.Fatalf("SaveMark: %v", err)
	}

	cmd := newLogsCommand(services)
	cmd.SetArgs([]string{"@api"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("kx logs @api: %v", err)
	}

	if len(kube.interactive) != 1 {
		t.Fatalf("kubectl invoked %d times, want 1", len(kube.interactive))
	}
	got := joinArgs(kube.interactive[0])
	if !strings.Contains(got, "api-7d8f") || !strings.Contains(got, "-n prod") {
		t.Errorf("kubectl args = %q, want the marked resource api-7d8f in prod", got)
	}
}

// An unknown mark is refused with resolveMark's own message — never with the
// generic "not a valid int" a parse failure would produce. A live run of
// `kx logs @nope` cannot tell "the parser choked on '@nope'" apart from "the
// mark 'nope' doesn't exist", since both fail the command the same way; the
// message text can, and only the second is correct, so this checks the text
// itself rather than just the exit code.
func TestLogsReportsAnUnknownMarkNotAParseFailure(t *testing.T) {
	kube := &recordingKubectl{}
	services := switchServices(t, kube)

	cmd := newLogsCommand(services)
	cmd.SetArgs([]string{"@nope"})
	err := cmd.Execute()
	if err == nil {
		t.Fatal("kx logs @nope succeeded, want the unknown-mark error")
	}
	if strings.Contains(err.Error(), "not a valid int") {
		t.Errorf("err = %q, want the unknown-mark message, not a parse failure", err)
	}
	if !strings.Contains(err.Error(), "No mark named 'nope'") {
		t.Errorf("err = %q, want it to name the missing mark", err)
	}
	if len(kube.interactive) != 0 {
		t.Errorf("kubectl was invoked %d times, want 0 — refused before any call", len(kube.interactive))
	}
}

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

// The spec's own motivating example: `kx labels 3 @api` should dedupe to one
// resource, not print it twice, when index 3 and mark @api name the same
// thing. Both existing dedupe tests use two indexes, and indexedResolver used
// to discard ref.Mark entirely, so this exact case could not be written
// against it before now.
func TestResolveRefsDedupesAnIndexAndAMarkNamingTheSameResource(t *testing.T) {
	resolver := refOf(
		[3]string{"web", "prod", "Pod"},
		[3]string{"db", "prod", "Pod"},
		[3]string{"api", "prod", "Pod"},
	)
	resolver.marks = map[string]int{"api": 3}

	resolved, err := resolveRefs(resolver, "indexes", []string{"3", "@api"})
	if err != nil {
		t.Fatalf("resolveRefs: %v", err)
	}
	if len(resolved) != 1 {
		t.Fatalf("resolved %d references, want 1 — index 3 and @api are one resource",
			len(resolved))
	}
	if resolved[0].Name != "api" {
		t.Errorf("resolved = %+v, want api", resolved)
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
