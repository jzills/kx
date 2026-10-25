package cli

import (
	"fmt"
	"sort"
	"strings"

	"github.com/jzills/kx/internal/docs"
	"github.com/jzills/kx/internal/render"
	"github.com/spf13/cobra"
)

// kx docs prints the documentation kx carries, with no agent and no browser.
//
// The same corpus kx mcp answers from (internal/docs), read a second way: an
// agent gets the sections that matched a question, a person at a prompt gets
// the page. It is the one command that answers without a cluster, a kubeconfig
// or a saved listing, which is the point of it — a jump host with no browser
// is exactly where the flag you have forgotten is hardest to look up.
func newDocsCommand(Services) *cobra.Command {
	return &cobra.Command{
		Use:   "docs [topic]",
		Short: "Read kx's documentation in the terminal: a topic, or the list of them.",
		Long: "Prints a documentation page from the copy built into this binary.\n\n" +
			"With no topic, lists every page by section. A topic is a page's path — " +
			"`concepts/state` — or just its last part where that is unambiguous, so " +
			"`kx docs top` reaches the reference page for `kx top`.\n\n" +
			"A topic that names no page is searched for instead, and the closest pages " +
			"are listed. 'state' names two, a concept and a command, so both are offered " +
			"rather than one of them chosen.\n\n" +
			"The pages describe the kx that is printing them, not the current website: " +
			"they are built into the binary, so they work offline and never describe a " +
			"flag this version does not have. `kx <command> --help` remains the shorter " +
			"answer for one command's flags.",
		Example: "  kx docs\n  kx docs top\n  kx docs concepts/state\n  kx docs marks",
		Args:    maxArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			if len(args) == 0 {
				return listDocsTopics()
			}
			return showDocsTopic(args[0])
		},
	}
}

// listDocsTopics prints every page, grouped the way the site groups them.
func listDocsTopics() error {
	pages, err := docs.Topics()
	if err != nil {
		return err
	}

	// Grouped and rendered a section at a time: Detail aligns the pairs it is
	// given, so one call per page would align each against itself and print a
	// ragged left edge.
	section := ""
	var rows [][2]string
	flush := func() {
		if len(rows) == 0 {
			return
		}
		render.Raw(render.Detail(rows))
		rows = nil
	}
	for _, page := range pages {
		if page.Section != section {
			flush()
			section = page.Section
			render.Blank()
			render.Section(section)
		}
		rows = append(rows, [2]string{page.Slug(), page.Description})
	}
	flush()
	render.Blank()
	render.Caption("kx docs <topic> for a page", fmt.Sprintf("%d pages", len(pages)))
	return nil
}

// showDocsTopic prints the page a topic names, or offers the ones it could
// have meant.
func showDocsTopic(topic string) error {
	found, err := docs.Find(topic)
	if err != nil {
		return err
	}

	switch len(found) {
	case 1:
		renderDocsPage(found[0])
		return nil
	case 0:
		return suggestDocsTopics(topic)
	default:
		render.Caption(fmt.Sprintf("'%s' names %d pages", topic, len(found)))
		render.Blank()
		offerDocsPages(found)
		return nil
	}
}

// suggestDocsTopics answers a topic that names no page with the pages a search
// for it finds, so a half-remembered word still reaches the documentation.
func suggestDocsTopics(topic string) error {
	results, err := docs.Search(topic, 5)
	if err != nil {
		return err
	}
	if len(results) == 0 {
		return fmt.Errorf("No documentation for '%s' — run 'kx docs' to list the topics.", topic)
	}

	render.Caption(fmt.Sprintf("No page named '%s'", topic), "closest matches")
	render.Blank()
	pages := make([]docs.Page, 0, len(results))
	for _, result := range results {
		pages = append(pages, result.Page)
	}
	offerDocsPages(pages)
	return nil
}

func offerDocsPages(pages []docs.Page) {
	rows := make([][2]string, 0, len(pages))
	for _, page := range pages {
		rows = append(rows, [2]string{page.Slug(), page.Description})
	}
	render.Raw(render.Detail(rows))
	render.Blank()
	render.Caption("kx docs <topic> to read one")
}

// renderDocsPage prints one page: its headings styled, everything else as the
// Markdown it is.
//
// Deliberately shallow. Headings earn their styling because they are how a
// reader finds the part they came for in a long reference page; tables and
// nested lists would cost a Markdown renderer to maintain and read no better
// here than they do as text. A fenced block's own '#' lines are comments in a
// shell example, and styling them as headings would be a lie about the shape
// of the page.
func renderDocsPage(page docs.Page) {
	render.Blank()
	render.Section(page.Title)
	if page.Description != "" {
		render.Caption(page.Description)
	}
	render.Blank()

	fenced := false
	for _, line := range strings.Split(strings.TrimRight(page.Body, "\n"), "\n") {
		if strings.HasPrefix(line, "```") {
			fenced = !fenced
			continue
		}
		if !fenced && strings.HasPrefix(line, "#") {
			render.Blank()
			render.Section(strings.TrimSpace(strings.TrimLeft(line, "#")))
			continue
		}
		render.Raw(line)
	}

	render.Blank()
	render.Caption(page.URL)
}

// completeDocsTopic completes a topic from the pages in the binary — the one
// completer that needs neither the cluster nor a saved listing.
//
// Both spellings are offered: the full path, and the last part alone where no
// other page ends in it, since that is what anyone types.
func completeDocsTopic(Services, string) []string {
	pages, err := docs.Topics()
	if err != nil {
		return nil
	}

	tails := map[string]int{}
	for _, page := range pages {
		tails[lastSegment(page.Slug())]++
	}

	var out []string
	for _, page := range pages {
		slug := page.Slug()
		out = append(out, slug+"\t"+page.Description)
		if tail := lastSegment(slug); tails[tail] == 1 && tail != slug {
			out = append(out, tail+"\t"+page.Description)
		}
	}
	sort.Strings(out)
	return out
}

func lastSegment(slug string) string {
	if at := strings.LastIndex(slug, "/"); at >= 0 {
		return slug[at+1:]
	}
	return slug
}
