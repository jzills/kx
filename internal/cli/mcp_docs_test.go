package cli

import (
	"context"
	"strings"
	"testing"
)

// The docs tool answers from the binary, so it needs none of mcpDeps filled
// in. That is the point of it, and a handler that reached for the cluster or
// the state file would fail here rather than in front of a user.
func docsDeps(version string) mcpDeps { return mcpDeps{Version: version} }

// What an agent mid-task does: asks in the user's words and gets the page
// that answers, trimmed to the part that answers it, with somewhere to send
// the user to read the rest.
func TestMCPDocsAnswersAQuestion(t *testing.T) {
	_, out, err := docsDeps("v9.9.9").docs(context.Background(), nil,
		docsInput{Query: "how do I sort kx top by memory"})
	if err != nil {
		t.Fatalf("docs: %v", err)
	}

	if len(out.Pages) == 0 {
		t.Fatal("the question returned no pages")
	}
	if len(out.Pages) > docsPageLimit {
		t.Errorf("returned %d pages, want at most %d", len(out.Pages), docsPageLimit)
	}
	first := out.Pages[0]
	if first.Title != "kx top" {
		t.Errorf("first page is %q, want %q", first.Title, "kx top")
	}
	if !strings.Contains(first.Text, "--sort") {
		t.Errorf("the text does not carry the flag that was asked about:\n%s", first.Text)
	}
	if !strings.HasPrefix(first.URL, "https://") {
		t.Errorf("page url %q is not somewhere a user can be sent", first.URL)
	}
	// The version is how an agent says which kx it just described, and a
	// zero value would read as a kx with no version at all.
	if out.Version != "v9.9.9" {
		t.Errorf("Version = %q, want the server's own", out.Version)
	}
	if out.Query != "how do I sort kx top by memory" {
		t.Errorf("Query = %q, want the question as asked", out.Query)
	}
}

// With no question there is nothing to rank, so the tool lists the map: every
// page with its description, and no bodies — which is what makes the listing
// cheap enough to be the answer to "what is there".
func TestMCPDocsWithNoQueryListsEveryPage(t *testing.T) {
	for _, query := range []string{"", "   ", "how do I use the"} {
		_, out, err := docsDeps("v1").docs(context.Background(), nil, docsInput{Query: query})
		if err != nil {
			t.Fatalf("docs(%q): %v", query, err)
		}
		if len(out.Pages) < 50 {
			t.Errorf("docs(%q) listed %d pages, want the whole map", query, len(out.Pages))
		}
		for _, page := range out.Pages {
			if page.Text != "" {
				t.Errorf("docs(%q) sent %q's body in a listing", query, page.Title)
				break
			}
		}
	}

	// The control for the "no bodies" assertion: a real question does send
	// them, so the check above cannot be passing on a tool that never fills
	// Text at all.
	_, out, err := docsDeps("v1").docs(context.Background(), nil, docsInput{Query: "marks"})
	if err != nil {
		t.Fatalf("docs: %v", err)
	}
	if len(out.Pages) == 0 || out.Pages[0].Text == "" {
		t.Errorf("a real question returned no page text: %+v", out.Pages)
	}
}

// A question nothing matches comes back empty rather than with the whole
// corpus. An agent reading "no pages" asks differently; an agent handed 61
// pages has spent its context to learn nothing.
func TestMCPDocsReturnsNothingForAnUnrelatedQuestion(t *testing.T) {
	_, out, err := docsDeps("v1").docs(context.Background(), nil,
		docsInput{Query: "xyzzy plugh frotz"})
	if err != nil {
		t.Fatalf("docs: %v", err)
	}
	if len(out.Pages) != 0 {
		t.Errorf("an unrelated question returned %d pages", len(out.Pages))
	}
}
