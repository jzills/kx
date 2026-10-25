package cli

import (
	"context"

	"github.com/jzills/kx/internal/docs"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// The docs tool: kx's own documentation, answered out of the binary.
//
// It is the one tool on this server that touches neither the cluster nor kx's
// state. Everything it reads is embedded at build time and never changes while
// the process runs, so it does not go through serialized — there is nothing
// for a concurrent call to race. That is also why it is cheap enough to call
// speculatively, which is how an agent part-way through a task will use it.

// docsPageLimit is how many pages a search returns. Three, because an agent
// pays for every one of them in context, and a question the right page does
// not answer is usually a question for a different search rather than a
// deeper list.
const docsPageLimit = 3

type docsInput struct {
	Query string `json:"query,omitempty" jsonschema:"What to look up, in the user's own words — 'sort top by memory', 'what a mark is', '--show-healthy'. Omit to list every page instead."`
}

// docsPage is one page of the answer. The URL is carried so an agent can hand
// the user somewhere to read the whole thing, and the excerpt is the part of
// the page that matched rather than the page entire.
type docsPage struct {
	Title       string `json:"title"`
	Section     string `json:"section"`
	URL         string `json:"url"`
	Description string `json:"description"`
	Text        string `json:"text,omitempty"`
}

type docsOutput struct {
	// Version is the kx these pages document, which is the kx running this
	// server rather than whatever the website has moved on to.
	Version string     `json:"version"`
	Query   string     `json:"query,omitempty"`
	Pages   []docsPage `json:"pages"`
}

func (d mcpDeps) docs(_ context.Context, _ *mcp.CallToolRequest, in docsInput) (*mcp.CallToolResult, docsOutput, error) {
	out := docsOutput{Version: d.Version, Query: in.Query, Pages: []docsPage{}}

	if terms := docs.Terms(in.Query); len(terms) == 0 {
		// No query, or a query of nothing but words that match everything:
		// the map, so the agent can ask again for a page by name. Listing
		// beats guessing from a term like "the".
		pages, err := docs.Pages()
		if err != nil {
			return nil, docsOutput{}, err
		}
		for _, page := range pages {
			out.Pages = append(out.Pages, docsPage{
				Title:       page.Title,
				Section:     page.Section,
				URL:         page.URL,
				Description: page.Description,
			})
		}
		return nil, out, nil
	}

	results, err := docs.Search(in.Query, docsPageLimit)
	if err != nil {
		return nil, docsOutput{}, err
	}
	for _, result := range results {
		out.Pages = append(out.Pages, docsPage{
			Title:       result.Title,
			Section:     result.Section,
			URL:         result.URL,
			Description: result.Description,
			Text:        result.Excerpt,
		})
	}
	return nil, out, nil
}
