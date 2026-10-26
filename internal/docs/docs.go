// Package docs holds kx's own documentation, embedded in the binary, and the
// lookup that finds the part of it a question is about.
//
// It is here so an agent driving `kx mcp` can answer a question about kx
// itself without kx shipping a model: the agent is the model, and this hands
// it the right pages. Embedded rather than fetched, because `kx mcp` has to
// work offline and against air-gapped clusters, and because documentation
// pinned to the binary describes the flags that binary accepts rather than
// the ones the website has moved on to.
//
// corpus.json is generated from site/content/docs by tools/gen-docs-context,
// the same run that writes the site's llms.txt and llms-full.txt. Edit the
// documentation, not this.
package docs

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"sync"
	"unicode"
)

//go:embed corpus.json
var corpusJSON []byte

// Page is one documentation page: its prose, and where the same page is
// published for a reader who would rather have the browser.
type Page struct {
	Title       string `json:"title"`
	Section     string `json:"section"`
	URL         string `json:"url"`
	Description string `json:"description"`
	Body        string `json:"body"`
}

var (
	once    sync.Once
	loaded  []Page
	loadErr error
)

// Pages is the whole corpus, in the order the site's navigation shows it.
//
// Decoded once and shared, never copied per call: it is tens of thousands of
// words, and `kx mcp` answers from it repeatedly in one session. Callers read
// it and do not write to it.
func Pages() ([]Page, error) {
	once.Do(func() {
		if err := json.Unmarshal(corpusJSON, &loaded); err != nil {
			loadErr = fmt.Errorf("embedded documentation: %w", err)
			return
		}
		if len(loaded) == 0 {
			loadErr = fmt.Errorf("embedded documentation is empty")
		}
	})
	return loaded, loadErr
}

// Result is one page a search matched, trimmed to the part that matched.
type Result struct {
	Page
	// Excerpt is the sections of the page the terms were found in, or the
	// whole body where the page is short enough to be worth sending entire.
	Excerpt string
	score   int
}

// wholePage is the body length below which a match returns the page rather
// than the sections of it that matched. A short page cut into sections loses
// the sentence before the heading as often as it saves anything, and the
// saving is not worth having at this size.
const wholePage = 2000

// Search returns the pages a question is about, best first, at most limit of
// them.
//
// Deliberately a keyword score rather than anything learned: the corpus is 61
// pages, the vocabulary is kx's own commands and flags, and a reader asking
// "how do I sort top by memory" uses the words the page uses. Embeddings here
// would be machinery to maintain for a corpus small enough to read.
func Search(query string, limit int) ([]Result, error) {
	pages, err := Pages()
	if err != nil {
		return nil, err
	}

	terms := Terms(query)
	if len(terms) == 0 {
		return nil, nil
	}

	var results []Result
	for _, page := range pages {
		// A section's own page is an introduction whose body is the
		// navigation listing the site draws and this corpus does not carry.
		// It has nothing to answer a question with.
		if strings.TrimSpace(page.Body) == "" {
			continue
		}
		if score := score(page, terms); score > 0 {
			results = append(results, Result{
				Page:    page,
				Excerpt: excerpt(page, terms),
				score:   score,
			})
		}
	}

	// Score first, then the site's own order, so a tie between two command
	// pages does not come back in a different order on a different run.
	sort.SliceStable(results, func(i, j int) bool {
		return results[i].score > results[j].score
	})
	if limit > 0 && len(results) > limit {
		results = results[:limit]
	}
	return results, nil
}

// skipped are words that match everything and so distinguish nothing. kx's
// own name is in the list for the same reason: every page is about kx, and a
// question about kx says so.
var skipped = map[string]bool{
	"a": true, "an": true, "and": true, "are": true, "by": true, "can": true,
	"do": true, "does": true, "for": true, "from": true, "how": true, "i": true,
	"in": true, "is": true, "it": true, "kx": true, "me": true, "my": true,
	"of": true, "on": true, "or": true, "that": true, "the": true, "to": true,
	"use": true, "what": true, "when": true, "where": true, "with": true,
	"you": true, "your": true,
}

// Terms splits a question into the words worth matching on: lowercased, with
// punctuation and the words above dropped. A flag keeps its dashes, so
// "--sort" and "sort" are the same term and a question can be asked either
// way.
func Terms(query string) []string {
	var terms []string
	seen := map[string]bool{}
	for _, field := range strings.FieldsFunc(strings.ToLower(query), func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r) && r != '-' && r != '_'
	}) {
		term := strings.Trim(field, "-_")
		if len(term) < 2 || skipped[term] || seen[term] {
			continue
		}
		seen[term] = true
		terms = append(terms, term)
	}
	return terms
}

// score weighs where a term was found, not how often. A term in a title says
// the page is about it; the tenth occurrence in a body says the page is long.
func score(page Page, terms []string) int {
	var (
		title       = strings.ToLower(page.Title)
		description = strings.ToLower(page.Description)
		body        = strings.ToLower(page.Body)
		total       int
	)
	for _, term := range terms {
		switch {
		case strings.Contains(title, term):
			total += 8
		case strings.Contains(description, term):
			total += 4
		case strings.Contains(body, term):
			total += 1
		}
	}
	return total
}

// excerpt returns the sections of a page the terms appear in, so a 400-line
// reference page answers a question about one flag with the part about that
// flag. Headings are kept with their sections: without them the reply is prose
// with nothing saying what it is about.
func excerpt(page Page, terms []string) string {
	if len(page.Body) <= wholePage {
		return page.Body
	}

	sections := split(page.Body)
	var kept []string
	for _, section := range sections {
		lower := strings.ToLower(section)
		for _, term := range terms {
			if strings.Contains(lower, term) {
				kept = append(kept, section)
				break
			}
		}
	}
	if len(kept) == 0 {
		return page.Body
	}

	// The opening, before the first heading, introduces the page: it is worth
	// having whenever anything else is.
	if opening := sections[0]; !strings.HasPrefix(opening, "#") && kept[0] != opening {
		kept = append([]string{opening}, kept...)
	}
	return strings.TrimSpace(strings.Join(kept, "\n\n")) + "\n"
}

// split cuts a body at its headings, each section keeping the heading that
// opens it. The text before the first heading is the first section.
func split(body string) []string {
	var (
		sections []string
		current  []string
		fenced   bool
	)
	for _, line := range strings.Split(body, "\n") {
		if strings.HasPrefix(line, "```") {
			fenced = !fenced
		}
		// A heading inside a fenced block is a comment in a shell example,
		// not a heading, and cutting there splits a command off its output.
		if !fenced && strings.HasPrefix(line, "#") && len(current) > 0 {
			sections = append(sections, strings.TrimSpace(strings.Join(current, "\n")))
			current = nil
		}
		current = append(current, line)
	}
	if len(current) > 0 {
		sections = append(sections, strings.TrimSpace(strings.Join(current, "\n")))
	}
	return sections
}

// Slug is the topic a page is reached by on the command line: its path under
// the documentation, without the site around it — "concepts/state",
// "reference/commands/top". Derived from the URL rather than stored, so it
// cannot disagree with where the page actually is.
func (p Page) Slug() string {
	path := p.URL
	if _, after, found := strings.Cut(path, "/docs/"); found {
		path = after
	}
	return strings.Trim(path, "/")
}

// Find returns the pages a topic names: the one whose slug is exactly it, or
// failing that every page whose slug ends in it, so "top" reaches
// reference/commands/top without anyone typing the path.
//
// Several, rather than a best guess, because the ambiguity is real: "state" is
// both a concept and a command, and answering one of them silently is the
// wrong half of the answer as often as not.
func Find(topic string) ([]Page, error) {
	pages, err := Pages()
	if err != nil {
		return nil, err
	}

	topic = strings.Trim(strings.ToLower(strings.TrimSpace(topic)), "/")
	if topic == "" {
		return nil, nil
	}

	var suffix []Page
	for _, page := range pages {
		slug := strings.ToLower(page.Slug())
		if slug == topic {
			return []Page{page}, nil
		}
		if strings.HasSuffix(slug, "/"+topic) {
			suffix = append(suffix, page)
		}
	}
	return suffix, nil
}

// Topics is every page that can be named on the command line, in the site's
// own order.
//
// The documentation's own front page is left out: it publishes at /docs/ and
// so has no slug to name it by, and what it holds is the navigation the site
// draws rather than anything to read.
func Topics() ([]Page, error) {
	pages, err := Pages()
	if err != nil {
		return nil, err
	}
	topics := make([]Page, 0, len(pages))
	for _, page := range pages {
		if page.Slug() != "" {
			topics = append(topics, page)
		}
	}
	return topics, nil
}
