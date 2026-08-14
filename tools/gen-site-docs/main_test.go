package main

import (
	"regexp"
	"strings"
	"testing"

	"github.com/jzills/kx/internal/cli"
	"github.com/jzills/kx/tools/internal/cmddoc"
)

func pages(t *testing.T) map[string]string {
	t.Helper()
	rendered, err := Pages()
	if err != nil {
		t.Fatalf("Pages() returned an error: %v", err)
	}
	return rendered
}

// A command reachable from the help screen but missing a page is a command
// nobody can look up on the site.
func TestEveryCommandHasAPage(t *testing.T) {
	rendered := pages(t)
	for _, name := range cli.CommandOrder() {
		if _, ok := rendered[name+".md"]; !ok {
			t.Errorf("no page generated for %q", name)
		}
	}
	// +1 for the section index, which is not a command.
	if want := len(cli.CommandOrder()) + 1; len(rendered) != want {
		t.Errorf("generated %d pages, want %d", len(rendered), want)
	}
}

// Every row of the overview has to reach a page that exists, or the reference
// is a table of dead links.
func TestOverviewLinksResolve(t *testing.T) {
	rendered := pages(t)
	index, ok := rendered["_index.md"]
	if !ok {
		t.Fatal("no _index.md generated")
	}

	links := regexp.MustCompile(`\]\(([^)]+)/\)`).FindAllStringSubmatch(index, -1)
	if len(links) != len(cli.CommandOrder()) {
		t.Errorf("overview has %d links, want %d", len(links), len(cli.CommandOrder()))
	}
	for _, link := range links {
		if _, ok := rendered[link[1]+".md"]; !ok {
			t.Errorf("overview links to %q, which no page provides", link[1])
		}
	}
}

// The root help screen's grouping is the site's grouping; a command in one and
// not the other means the two disagree about what kx offers.
func TestOverviewCoversEverySection(t *testing.T) {
	index := pages(t)["_index.md"]
	for _, section := range cli.HelpSections() {
		if !strings.Contains(index, "\n## "+section.Title+"\n") {
			t.Errorf("overview is missing the %q section", section.Title)
		}
	}
}

// goldmark runs with unsafe HTML on, so an unescaped angle bracket in a
// description is parsed as a tag and vanishes from the page. `kx get`'s summary
// contains "kx <kind>", which is exactly that case.
func TestTableCellsEscapeMarkup(t *testing.T) {
	for name, body := range pages(t) {
		for _, line := range strings.Split(body, "\n") {
			if !strings.HasPrefix(line, "| ") {
				continue
			}
			// The command column is a code span, where a bracket is literal
			// and a pipe cannot occur; only the description is prose.
			_, description, found := strings.Cut(strings.TrimSuffix(line, " |"), "` | ")
			if !found {
				continue
			}
			if strings.ContainsAny(description, "<>") {
				t.Errorf("%s: unescaped angle bracket in a table cell: %s", name, line)
			}
			if strings.Contains(description, "|") && !strings.Contains(description, `\|`) {
				t.Errorf("%s: unescaped pipe in a table cell: %s", name, line)
			}
		}
	}
}

// Front matter carries descriptions with colons ("shorthand: kx <kind>"), which
// end a plain YAML scalar. Quoted, they parse; unquoted, the file does not.
func TestFrontMatterIsQuoted(t *testing.T) {
	for name, body := range pages(t) {
		for _, key := range []string{"title:", "linkTitle:", "description:"} {
			line := frontMatterLine(t, name, body, key)
			value := strings.TrimSpace(strings.TrimPrefix(line, key))
			if !strings.HasPrefix(value, `"`) || !strings.HasSuffix(value, `"`) {
				t.Errorf("%s: %s is not a quoted scalar: %s", name, key, line)
			}
		}
	}
}

// The prose body is written straight through, on the assumption that the Long
// strings keep their angle brackets inside code spans — `-n <namespace>` does.
// One that does not would disappear from the rendered page silently, so the
// assumption is asserted rather than trusted.
func TestProseAngleBracketsStayInCodeSpans(t *testing.T) {
	byName := cmddoc.Commands()
	for _, name := range cli.CommandOrder() {
		doc := cli.HelpFor(byName[name]).Doc
		if stripped := regexp.MustCompile("`[^`]*`").ReplaceAllString(doc, ""); strings.ContainsAny(stripped, "<>") {
			t.Errorf("kx %s: angle bracket outside a code span in its description: %q", name, doc)
		}
	}
}

// Pages are compared byte for byte against what is on disk to decide whether to
// rewrite them, so an unstable render would report a change on every run and
// fail CI on a clean tree.
func TestRenderIsDeterministic(t *testing.T) {
	first, second := pages(t), pages(t)
	for name, body := range first {
		if second[name] != body {
			t.Errorf("%s renders differently on a second call", name)
		}
	}
}

func frontMatterLine(t *testing.T, name, body, key string) string {
	t.Helper()
	for _, line := range strings.Split(body, "\n") {
		if strings.HasPrefix(line, key) {
			return line
		}
	}
	t.Fatalf("%s: no %s line in front matter", name, key)
	return ""
}

func TestSignatureMatchesTheREADMESpelling(t *testing.T) {
	// One row, spelled out, so a change to the shared renderer has to be
	// deliberate rather than silently reshaping both generated tables.
	cmd, ok := cmddoc.Commands()["scale"]
	if !ok {
		t.Fatal("no scale command registered")
	}
	if got, want := cmddoc.Signature(cmd), "kx scale <index> <replicas>"; got != want {
		t.Errorf("Signature() = %q, want %q", got, want)
	}
}
