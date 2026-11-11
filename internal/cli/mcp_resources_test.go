package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/jzills/kx/internal/docs"
	"github.com/jzills/kx/internal/kinds"
	"github.com/jzills/kx/internal/state"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// readResource reads one resource through the protocol and returns its text.
func readResource(t *testing.T, session *mcp.ClientSession, uri string) string {
	t.Helper()
	result, err := session.ReadResource(context.Background(), &mcp.ReadResourceParams{URI: uri})
	if err != nil {
		t.Fatalf("ReadResource %s: %v", uri, err)
	}
	if len(result.Contents) != 1 || result.Contents[0].URI != uri {
		t.Fatalf("ReadResource %s: contents = %+v, want one entry for that URI", uri, result.Contents)
	}
	return result.Contents[0].Text
}

// seedListing saves a two-row listing as the user's current entry.
func seedListing(t *testing.T, deps mcpDeps) {
	t.Helper()
	if err := deps.State.Save(state.State{
		Resources: state.NewOrderedResources([]state.Resource{
			{Name: "api", Kind: kinds.Deployment, Namespace: "prod"},
			{Name: "web", Kind: kinds.Deployment, Namespace: "prod"},
		}),
		Namespace: "prod",
		Query:     &state.Query{Resource: "deploy", Args: []string{}},
	}); err != nil {
		t.Fatalf("Save: %v", err)
	}
}

// With --write-listings off, no index appears in anything the server
// returns, and kx://listing is no exception: a client can read it for the
// agent as well as attach it for the user. The rows are there, in order, by
// kind and name. The flag-on read below is the control, so a fixture that
// could never produce an index cannot pass this.
func TestListingResourceCarriesIndexesOnlyWithWriteListings(t *testing.T) {
	for _, write := range []bool{false, true} {
		deps := mcpTestDeps(t, &recordingKubectl{})
		deps.WriteListings = write
		seedListing(t, deps)

		var document listingDocument
		text := readResource(t, connectMCP(t, deps), resourceListingURI)
		if err := json.Unmarshal([]byte(text), &document); err != nil {
			t.Fatalf("write=%v: %v\n%s", write, err, text)
		}
		if document.Listing == nil || len(document.Listing.Resources) != 2 {
			t.Fatalf("write=%v: listing = %+v, want the two seeded rows", write, document.Listing)
		}
		if first := document.Listing.Resources[0]; first.Kind != "Deployment" || first.Name != "api" {
			t.Errorf("write=%v: first row = %+v, want Deployment api", write, first)
		}
		hasIndex := strings.Contains(text, `"index":`)
		if hasIndex != write {
			t.Errorf("write=%v: index present = %v in\n%s", write, hasIndex, text)
		}
		if write && document.Listing.Resources[1].Index != 2 {
			t.Errorf("second row's index = %d, want 2", document.Listing.Resources[1].Index)
		}
		if q := document.Listing.Query; q == nil || q.Command != "get" || q.Resource != "deploy" {
			t.Errorf("write=%v: query = %+v, want kx get deploy", write, q)
		}
	}
}

// Before any kx get there is no listing, which is an answer rather than an
// error: a client attaching @kx:kx://listing on a fresh install should see
// that there is nothing listed, not a failed read.
func TestListingResourceWithNoHistoryIsNull(t *testing.T) {
	text := readResource(t, connectMCP(t, mcpTestDeps(t, &recordingKubectl{})), resourceListingURI)
	if !strings.Contains(text, `"listing": null`) || !strings.Contains(text, `"context": "test"`) {
		t.Errorf("kx://listing with no history =\n%s\nwant a null listing beside the current context", text)
	}
}

// Reading a resource writes nothing: the state file is byte for byte what it
// was, with the flag on, where the listing tools would save.
func TestResourcesWriteNothing(t *testing.T) {
	deps := mcpTestDeps(t, &recordingKubectl{})
	deps.WriteListings = true
	seedListing(t, deps)
	before, err := os.ReadFile(deps.State.Path)
	if err != nil {
		t.Fatal(err)
	}
	session := connectMCP(t, deps)
	for _, uri := range []string{resourceContextURI, resourceMarksURI, resourceListingURI} {
		readResource(t, session, uri)
	}
	after, err := os.ReadFile(deps.State.Path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) {
		t.Errorf("state file changed across resource reads:\nbefore %s\nafter  %s", before, after)
	}
}

func TestContextResourceNamesWhereToolsReach(t *testing.T) {
	deps := mcpTestDeps(t, &recordingKubectl{namespace: "staging"})
	deps.WriteListings = true
	var document contextDocument
	text := readResource(t, connectMCP(t, deps), resourceContextURI)
	if err := json.Unmarshal([]byte(text), &document); err != nil {
		t.Fatal(err)
	}
	want := contextDocument{Version: "1.2.3", Context: "test", Namespace: "staging", WriteListings: true}
	if document != want {
		t.Errorf("kx://context = %+v, want %+v", document, want)
	}
}

// kx://marks holds what list_marks returns, so attaching it and calling the
// tool cannot disagree.
func TestMarksResourceMatchesListMarks(t *testing.T) {
	deps := mcpTestDeps(t, &recordingKubectl{})
	if err := deps.State.SaveMark("culprit", state.Mark{Resource: state.Resource{Name: "api", Kind: kinds.Deployment, Namespace: "prod"}}); err != nil {
		t.Fatal(err)
	}
	session := connectMCP(t, deps)
	text := readResource(t, session, resourceMarksURI)
	if !strings.Contains(text, `"name": "culprit"`) {
		t.Fatalf("kx://marks =\n%s\nwant the culprit mark", text)
	}
	call, err := session.CallTool(context.Background(), &mcp.CallToolParams{Name: "list_marks"})
	if err != nil {
		t.Fatal(err)
	}
	var fromResource, fromTool listMarksOutput
	if err := json.Unmarshal([]byte(text), &fromResource); err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(call.StructuredContent)
	if err := json.Unmarshal(raw, &fromTool); err != nil {
		t.Fatal(err)
	}
	a, _ := json.Marshal(fromResource)
	b, _ := json.Marshal(fromTool)
	if !bytes.Equal(a, b) {
		t.Errorf("kx://marks = %s, list_marks = %s", a, b)
	}
}

// Every page kx docs can name is a resource, so the @ menu offers the same
// set the docs command does, and each reads back as that page's Markdown.
func TestEveryDocsTopicIsAResource(t *testing.T) {
	topics, err := docs.Topics()
	if err != nil {
		t.Fatal(err)
	}
	session := connectMCP(t, mcpTestDeps(t, &recordingKubectl{}))
	listed := map[string]bool{}
	for resource, err := range session.Resources(context.Background(), nil) {
		if err != nil {
			t.Fatal(err)
		}
		listed[resource.URI] = true
	}
	for _, page := range topics {
		if uri := resourceDocsPrefix + page.Slug(); !listed[uri] {
			t.Errorf("%s is a docs topic with no resource", uri)
		}
	}
	if len(listed) != len(topics)+3 {
		t.Errorf("resources = %d, want %d docs pages and context, marks and listing", len(listed), len(topics))
	}

	page := topics[0]
	text := readResource(t, session, resourceDocsPrefix+page.Slug())
	if !strings.HasPrefix(text, "# "+page.Title+"\n") || !strings.Contains(text, page.URL) {
		t.Errorf("%s = %.200q, want the page under its title and its URL", page.Slug(), text)
	}
}
