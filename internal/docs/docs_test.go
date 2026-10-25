package docs

import (
	"strings"
	"testing"
)

// The corpus is embedded at build time, so an empty or unparseable one is a
// build that shipped without its documentation — and every other test here
// would pass vacuously against it.
func TestTheCorpusIsEmbedded(t *testing.T) {
	pages, err := Pages()
	if err != nil {
		t.Fatalf("Pages: %v", err)
	}
	if len(pages) < 50 {
		t.Fatalf("corpus holds %d pages; the site has upwards of 60", len(pages))
	}

	withBody := 0
	for _, page := range pages {
		if page.Title == "" || page.URL == "" {
			t.Errorf("a page carries no title or url: %+v", page)
		}
		if !strings.HasPrefix(page.URL, "https://") {
			t.Errorf("%q's url is not absolute: %q", page.Title, page.URL)
		}
		if strings.TrimSpace(page.Body) != "" {
			withBody++
		}
	}
	// Titles and urls alone would pass on a corpus of empty pages.
	if withBody < 50 {
		t.Errorf("only %d pages carry any prose", withBody)
	}
}

// What the tool is for: a question in the user's own words reaching the page
// that answers it. These are the questions an agent actually asks mid-task,
// and the ranking is the whole value of the tool — a search that returned the
// right page fourth would be a search an agent pays for and ignores.
func TestSearchFindsThePageThatAnswers(t *testing.T) {
	for _, tc := range []struct{ query, want string }{
		{"how do I sort kx top by memory", "kx top"},
		{"what does a mark do", "Marks"},
		{"install with homebrew", "Install"},
		{"ephemeral container for a distroless pod", "kx debug"},
		{"where is state kept", "State and history"},
	} {
		results, err := Search(tc.query, 3)
		if err != nil {
			t.Fatalf("Search(%q): %v", tc.query, err)
		}
		if len(results) == 0 {
			t.Errorf("Search(%q) found nothing", tc.query)
			continue
		}
		if results[0].Title != tc.want {
			var got []string
			for _, result := range results {
				got = append(got, result.Title)
			}
			t.Errorf("Search(%q) ranked %v; want %q first", tc.query, got, tc.want)
		}
	}
}

// A question of nothing but words every page shares must not come back with a
// confident wrong answer. The tool lists the map instead, and that decision is
// made on Terms being empty.
func TestTermsDropsWordsThatMatchEverything(t *testing.T) {
	if terms := Terms("how do I use kx with the"); len(terms) != 0 {
		t.Errorf("Terms kept %v; every one of those words is on every page", terms)
	}
	// The control: a real question keeps its real words, so the check above
	// is not passing because Terms drops everything.
	terms := Terms("sort top by MEMORY --show-healthy")
	want := map[string]bool{"sort": true, "top": true, "memory": true, "show-healthy": true}
	if len(terms) != len(want) {
		t.Fatalf("Terms returned %v, want the four meaningful words", terms)
	}
	for _, term := range terms {
		if !want[term] {
			t.Errorf("Terms returned %q, which is not one of the question's words", term)
		}
	}
}

// A long reference page answered whole is a page an agent pays for in full to
// read one flag. The excerpt is the part that matched, and this pins that it
// is actually smaller — and still carries what was asked about.
func TestSearchTrimsALongPageToWhatMatched(t *testing.T) {
	results, err := Search("scan severity threshold fail-on", 1)
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(results) == 0 {
		t.Fatal("no result for a question the scan page answers")
	}

	result := results[0]
	if len(result.Body) <= wholePage {
		t.Skipf("%q is short enough to be returned whole; nothing to trim", result.Title)
	}
	if len(result.Excerpt) >= len(result.Body) {
		t.Errorf("%q was not trimmed: excerpt %d bytes of a %d-byte page",
			result.Title, len(result.Excerpt), len(result.Body))
	}
	if !strings.Contains(strings.ToLower(result.Excerpt), "fail-on") {
		t.Errorf("the excerpt dropped the thing that was asked about:\n%s", result.Excerpt)
	}
}

// A heading inside a fenced code block is a comment in a shell example. Cut
// there and a command is split from its output, which is the one shape an
// agent is most likely to copy and run.
func TestSplitIgnoresHeadingsInsideCodeFences(t *testing.T) {
	body := "Opening.\n\n```bash\n# not a heading\nkx get pods\n```\n\n## A heading\n\nUnder it.\n"
	sections := split(body)
	if len(sections) != 2 {
		t.Fatalf("split returned %d sections, want 2:\n%q", len(sections), sections)
	}
	if !strings.Contains(sections[0], "kx get pods") || !strings.Contains(sections[0], "# not a heading") {
		t.Errorf("the fenced example was cut up:\n%q", sections[0])
	}
	if !strings.HasPrefix(sections[1], "## A heading") {
		t.Errorf("the real heading did not open its own section:\n%q", sections[1])
	}
}

// Every command kx offers has a reference page on the site, and the corpus is
// generated from those pages. A command missing here is one an agent is told
// does not exist.
func TestEveryCommandPageIsInTheCorpus(t *testing.T) {
	pages, err := Pages()
	if err != nil {
		t.Fatalf("Pages: %v", err)
	}
	commands := 0
	for _, page := range pages {
		if page.Section == "Commands" {
			commands++
		}
	}
	if commands < 30 {
		t.Errorf("the corpus holds %d command pages; kx has more than 30 commands", commands)
	}
}
