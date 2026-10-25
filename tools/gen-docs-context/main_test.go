package main

import (
	"strings"
	"testing"

	"github.com/jzills/kx/internal/cli"
)

const testBase = "https://jzills.github.io/kx/"

// corpus loads the site's real pages, the way the generator does. The tests
// below that use it are checking the two files against the documentation as it
// actually stands, not against a fixture that can agree with a broken
// generator.
func corpus(t *testing.T) []Page {
	t.Helper()
	pages, err := load("../../"+contentDir, testBase)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if len(pages) == 0 {
		t.Fatalf("no pages loaded from %s", contentDir)
	}
	return pages
}

// A link in a page body is written relative to the page it sits on. Read out
// of that page — which is the only way these files are read — it points
// nowhere, and an agent following it gets a 404 instead of the install
// instructions.
//
// Both halves of this were real defects caught before the first commit. The
// resolution ran against a base of "/", and url.ResolveReference hands back
// the reference unchanged when its base is not absolute, so every link came
// through untouched. The fix exposed the second: a link whose text carries
// nested brackets, which the reference's command signatures all do.
func TestCleanResolvesRelativeLinksAgainstThePage(t *testing.T) {
	page := "https://jzills.github.io/kx/docs/concepts/completion/"
	body := "See the [install page](../../getting-started/install/).\n\n" +
		"And [`kx annotate <index> [<key=value>...] [--dry-run[=str]]`](../../reference/commands/annotate/).\n\n" +
		"Unchanged: [the spec](https://llmstxt.org/), [a heading](#themes).\n"

	got, err := Clean(body, page)
	if err != nil {
		t.Fatalf("Clean: %v", err)
	}

	for _, want := range []string{
		"(https://jzills.github.io/kx/docs/getting-started/install/)",
		"(https://jzills.github.io/kx/docs/reference/commands/annotate/)",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("relative link not resolved to %s:\n%s", want, got)
		}
	}
	// The control for the two assertions above: links that were already
	// absolute, and a fragment that points within the page, must survive
	// untouched — a rewrite that mangled them would still pass the checks.
	for _, want := range []string{"(https://llmstxt.org/)", "(#themes)"} {
		if !strings.Contains(got, want) {
			t.Errorf("%s was rewritten; it should have been left alone:\n%s", want, got)
		}
	}
}

// A shortcode is Hugo's, and means nothing to a reader holding only the text.
// Passing an unrecognized one through publishes literal template braces into a
// file an agent reads as prose, so the generator stops instead — the same
// bargain internal/mark makes when it meets a glyph it cannot draw.
func TestCleanRefusesAnUnknownShortcode(t *testing.T) {
	for _, body := range []string{
		"Before.\n\n{{< kx-invented foo=\"bar\" >}}\n\nAfter.\n",
		"Before.\n\n{{% kx-invented %}}inner{{% /kx-invented %}}\n\nAfter.\n",
	} {
		got, err := Clean(body, testBase)
		if err == nil {
			t.Errorf("Clean accepted an unknown shortcode and returned:\n%s", got)
			continue
		}
		if !strings.Contains(err.Error(), "kx-invented") {
			t.Errorf("error does not name the shortcode: %v", err)
		}
	}

	// The control: the shortcodes the site does use are still handled, so the
	// test above cannot be passing because Clean rejects everything.
	if _, err := Clean("{{< kx-children >}}\n\n{{% kx-note %}}x{{% /kx-note %}}\n", testBase); err != nil {
		t.Errorf("Clean rejected shortcodes it knows: %v", err)
	}
}

// An aside is prose. Dropping kx-note the way the navigation and screenshot
// shortcodes are dropped would silently delete a paragraph from the middle of
// a page, which no diff of the generator's output would make obvious.
func TestCleanKeepsANotesProseAndDropsTheWidgets(t *testing.T) {
	body := "Opening.\n\n{{% kx-note %}}\nMarks are shared with your terminal.\n{{% /kx-note %}}\n\n" +
		"{{< kx-children >}}\n\n{{< kx-shot report=\"diag\" alt=\"kx diag --html dashboard\" >}}\n\nClosing.\n"

	got, err := Clean(body, testBase)
	if err != nil {
		t.Fatalf("Clean: %v", err)
	}

	if !strings.Contains(got, "> Marks are shared with your terminal.") {
		t.Errorf("the note's prose is not in the output as a blockquote:\n%s", got)
	}
	if !strings.Contains(got, "> **Note**") {
		t.Errorf("the note lost its label:\n%s", got)
	}
	for _, unwanted := range []string{"kx-children", "kx-shot", "kx-note"} {
		if strings.Contains(got, unwanted) {
			t.Errorf("%q survived into the output:\n%s", unwanted, got)
		}
	}
	// The control for those three: the prose around them is still there, so
	// the assertions above cannot be passing on an empty result.
	for _, want := range []string{"Opening.", "Closing."} {
		if !strings.Contains(got, want) {
			t.Errorf("%q was dropped along with the shortcodes:\n%s", want, got)
		}
	}
}

// A warning is not a note, and a reader with no stylesheet can only tell them
// apart by what the text says.
func TestCleanLabelsAWarning(t *testing.T) {
	got, err := Clean("{{% kx-note kind=\"warn\" %}}\nDraining evicts Pods.\n{{% /kx-note %}}\n", testBase)
	if err != nil {
		t.Fatalf("Clean: %v", err)
	}
	if !strings.Contains(got, "> **Warning**") {
		t.Errorf("a warn note is not labeled as one:\n%s", got)
	}
}

// llms.txt is a map, and a page missing from it is a page an agent never
// learns exists. This runs against the real documentation, so a page added to
// the site without reaching the map fails here.
func TestIndexLinksEveryPage(t *testing.T) {
	pages := corpus(t)
	index := Index(pages, testBase)

	linked := 0
	for _, page := range pages {
		if page.Index {
			// A section's own page is represented by its H2 heading rather
			// than a link, so its title has to be a heading instead.
			if !strings.Contains(index, "\n## "+page.Title+"\n") && page.Dir != "" {
				t.Errorf("section %q has no heading in llms.txt", page.Title)
			}
			continue
		}
		link := "](" + page.URL(testBase) + ")"
		if !strings.Contains(index, link) {
			t.Errorf("llms.txt does not link %s", page.Rel)
			continue
		}
		linked++
	}
	if linked == 0 {
		t.Fatal("no page was linked, so the check above proved nothing")
	}
}

// llms-full.txt is the whole documentation, and the one thing it must not do
// is quietly hold less than it claims. A generator that walked the tree wrong
// would still produce a plausible file.
func TestFullHoldsEveryPage(t *testing.T) {
	pages := corpus(t)
	full := Full(pages, testBase)

	for _, page := range pages {
		if !strings.Contains(full, "Source: "+page.URL(testBase)+"\n") {
			t.Errorf("llms-full.txt is missing %s", page.Rel)
		}
	}
	// Titles alone would pass on a file of headings with no prose under them.
	for _, page := range pages {
		if body := strings.TrimSpace(page.Body); body != "" && !strings.Contains(full, body) {
			t.Errorf("llms-full.txt holds %s's heading but not its body", page.Rel)
		}
	}
}

// Every command kx offers has a reference page, generated by gen-site-docs
// from the command tree. This is the join between the two generators: a
// command that reaches the site but not the map is one an agent cannot look
// up, and the failure would otherwise be invisible — both files would still
// generate, and both would still look right.
func TestEveryCommandReachesTheMap(t *testing.T) {
	index := Index(corpus(t), testBase)
	for _, name := range cli.CommandOrder() {
		if url := testBase + "docs/reference/commands/" + name + "/"; !strings.Contains(index, url) {
			t.Errorf("llms.txt does not link the reference page for %q", name)
		}
	}
}

// The sections of llms.txt are the site's own navigation, in its own order. An
// agent reading the map top to bottom should meet getting-started before the
// command reference, as a reader does.
func TestSectionsFollowTheSiteOrder(t *testing.T) {
	index := Index(corpus(t), testBase)

	var order []int
	for _, heading := range []string{"## Getting started", "## Concepts", "## Guides", "## Reference"} {
		at := strings.Index(index, heading)
		if at < 0 {
			t.Fatalf("llms.txt has no %q section", heading)
		}
		order = append(order, at)
	}
	for i := 1; i < len(order); i++ {
		if order[i] < order[i-1] {
			t.Errorf("llms.txt puts its sections out of the site's order: %v", order)
			break
		}
	}
}

// Hugo publishes a page from a directory of its own and a section's _index.md
// from the section's, so the URL cannot be the file path with the extension
// taken off.
func TestPageURLsMatchWhereHugoPublishes(t *testing.T) {
	for _, tc := range []struct{ rel, want string }{
		{"guides/triage-a-namespace.md", testBase + "docs/guides/triage-a-namespace/"},
		{"guides/_index.md", testBase + "docs/guides/"},
		{"reference/commands/top.md", testBase + "docs/reference/commands/top/"},
		{"_index.md", testBase + "docs/"},
	} {
		page := Page{Rel: tc.rel, Index: strings.HasSuffix(tc.rel, indexName)}
		if got := page.URL(testBase); got != tc.want {
			t.Errorf("URL(%q) = %q, want %q", tc.rel, got, tc.want)
		}
	}
}

// Front matter is how every page carries its title, and a page without it
// would otherwise reach the map titled after nothing.
func TestParseRefusesAPageWithoutFrontMatter(t *testing.T) {
	for _, raw := range []string{
		"Just prose, no front matter.\n",
		"---\ntitle: Unclosed\n",
		"---\nweight: 3\n---\n\nNo title.\n",
	} {
		if page, err := Parse("x.md", raw, testBase); err == nil {
			t.Errorf("Parse accepted %q and returned %+v", raw, page)
		}
	}

	// The control: a well-formed page still parses, with its front matter read.
	page, err := Parse("guides/x.md", "---\ntitle: T\nlinkTitle: L\nweight: 2\n---\n\nBody.\n", testBase)
	if err != nil {
		t.Fatalf("Parse rejected a well-formed page: %v", err)
	}
	if page.Title != "T" || page.Name() != "L" || page.Weight != 2 ||
		!strings.Contains(page.Body, "Body.") {
		t.Errorf("front matter read wrong: %+v", page)
	}
}
