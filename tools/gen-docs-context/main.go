// Command gen-docs-context renders the site's documentation into the two
// files an agent reads instead of a web page.
//
//	go run ./tools/gen-docs-context
//
// Writes site/static/llms.txt, a Markdown map of the docs in the shape the
// llms.txt proposal describes, and site/static/llms-full.txt, every page's
// prose concatenated. Hugo copies static/ verbatim, so they publish at
// /llms.txt and /llms-full.txt.
//
// Neither file is a standard. llms.txt is an informal community proposal
// (llmstxt.org), and llms-full.txt is a convention other documentation
// platforms settled on that the proposal does not mention. They are generated
// because they are cheap and correct, not because anything requires them.
//
// Same contract as gen-site-docs: run by a pre-commit hook, which fails when a
// hook rewrites a tracked file, and by .github/workflows/pages.yml, which
// regenerates and diffs so a page edited without regenerating fails there too.
// Exits non-zero only on error, never to report that it rewrote something —
// swallowing a rewrote-a-file code would also swallow a broken generator,
// whose run writes nothing and leaves the diff clean.
//
// The one input is site/content/docs, which is also why the command reference
// needs no special handling here: gen-site-docs generates those pages from
// internal/cli, so they are already in step with the flags kx accepts, and
// this generator reads the result rather than the command tree a second time.
package main

import (
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/BurntSushi/toml"
	"github.com/jzills/kx/internal/cli"
	"gopkg.in/yaml.v3"
)

const (
	contentDir = "site/content/docs"
	configPath = "site/hugo.toml"
	indexPath  = "site/static/llms.txt"
	fullPath   = "site/static/llms-full.txt"
	// corpusPath is the same prose again, as data the binary embeds. It lives
	// under internal/docs because go:embed cannot reach outside its own
	// package's directory, and it is JSON rather than the Markdown file above
	// so a reader does not have to parse page boundaries back out of prose.
	corpusPath = "internal/docs/corpus.json"

	// indexName is the filename Hugo treats as a section's own page: it
	// publishes at the section's URL rather than one below it.
	indexName = "_index.md"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run() error {
	base, err := baseURL(configPath)
	if err != nil {
		return err
	}

	pages, err := load(contentDir, base)
	if err != nil {
		return err
	}
	if len(pages) == 0 {
		return fmt.Errorf("no pages under %s", contentDir)
	}

	corpus, err := Corpus(pages, base)
	if err != nil {
		return err
	}

	for _, out := range []struct {
		path    string
		content string
	}{
		{indexPath, Index(pages, base)},
		{fullPath, Full(pages, base)},
		{corpusPath, corpus},
	} {
		if err := os.WriteFile(out.path, []byte(out.content), 0o644); err != nil {
			return err
		}
		fmt.Println("updated", out.path)
	}
	return nil
}

// Page is one documentation page, with its front matter read and its body
// rewritten for a reader who has only the text.
type Page struct {
	// Rel is the path under contentDir, slash-separated, e.g.
	// "guides/triage-a-namespace.md".
	Rel string
	// Dir is the directory Rel sits in, "" for a page directly under
	// contentDir. It is what groups pages into sections.
	Dir string
	// Index marks a section's own _index.md.
	Index bool

	Title       string
	LinkTitle   string
	Description string
	Weight      int
	Body        string
}

// Name is the title to show in a link: the shorter one the site's navigation
// uses where a page carries it, since that is what it was written for.
func (p Page) Name() string {
	if p.LinkTitle != "" {
		return p.LinkTitle
	}
	return p.Title
}

// URL is where the page publishes. Hugo drops the extension and serves each
// page from a directory of its own, and an _index.md from its section's.
func (p Page) URL(base string) string {
	trimmed := strings.TrimSuffix(p.Rel, ".md")
	if p.Index {
		trimmed = path.Dir(trimmed)
		if trimmed == "." {
			trimmed = ""
		}
	}
	return strings.TrimSuffix(base, "/") + path.Join("/docs", trimmed) + "/"
}

// hugoConfig is the one key this generator needs from the site's config. Read
// rather than repeated here so the published URLs follow a baseURL change
// instead of having to be found afterwards.
type hugoConfig struct {
	BaseURL string `toml:"baseURL"`
}

func baseURL(configFile string) (string, error) {
	var config hugoConfig
	if _, err := toml.DecodeFile(configFile, &config); err != nil {
		return "", fmt.Errorf("%s: %w", configFile, err)
	}
	if config.BaseURL == "" {
		return "", fmt.Errorf("%s declares no baseURL", configFile)
	}
	return config.BaseURL, nil
}

// load reads every page under dir, in no particular order.
func load(dir, base string) ([]Page, error) {
	var pages []Page
	err := filepath.WalkDir(dir, func(name string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() || !strings.HasSuffix(name, ".md") {
			return nil
		}

		raw, err := os.ReadFile(name)
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(dir, name)
		if err != nil {
			return err
		}
		page, err := Parse(filepath.ToSlash(rel), string(raw), base)
		if err != nil {
			return err
		}
		pages = append(pages, page)
		return nil
	})
	return pages, err
}

// frontMatter is the subset of a page's front matter the two files use.
type frontMatter struct {
	Title       string `yaml:"title"`
	LinkTitle   string `yaml:"linkTitle"`
	Description string `yaml:"description"`
	Weight      int    `yaml:"weight"`
}

// Parse reads one page: its front matter, and its body with the Hugo left
// out. base is the site's own, which the body's relative links are resolved
// against: url.ResolveReference needs an absolute base, and silently hands
// back the reference itself when it does not get one.
func Parse(rel, raw, base string) (Page, error) {
	matter, body, err := split(raw)
	if err != nil {
		return Page{}, fmt.Errorf("%s: %w", rel, err)
	}

	var front frontMatter
	if err := yaml.Unmarshal([]byte(matter), &front); err != nil {
		return Page{}, fmt.Errorf("%s: front matter: %w", rel, err)
	}
	if front.Title == "" {
		return Page{}, fmt.Errorf("%s: front matter carries no title", rel)
	}

	dir := path.Dir(rel)
	if dir == "." {
		dir = ""
	}
	page := Page{
		Rel:         rel,
		Dir:         dir,
		Index:       path.Base(rel) == indexName,
		Title:       front.Title,
		LinkTitle:   front.LinkTitle,
		Description: front.Description,
		Weight:      front.Weight,
	}

	page.Body, err = Clean(body, page.URL(base))
	if err != nil {
		return Page{}, fmt.Errorf("%s: %w", rel, err)
	}
	return page, nil
}

// split separates YAML front matter from the body. A page without it is an
// error rather than a page with no title: every page the site publishes has
// front matter, so a missing block means the file was read wrong.
func split(raw string) (matter, body string, err error) {
	rest, found := strings.CutPrefix(strings.TrimLeft(raw, "\ufeff"), "---\n")
	if !found {
		return "", "", fmt.Errorf("no YAML front matter")
	}
	matter, body, found = strings.Cut(rest, "\n---\n")
	if !found {
		return "", "", fmt.Errorf("front matter is not closed")
	}
	return matter, body, nil
}

var (
	// A paired shortcode, open tag through close tag. Non-greedy so two of
	// them on one page stay two.
	pairedShortcode = regexp.MustCompile(`(?s)\{\{% *([a-zA-Z0-9_-]+)([^%]*)%\}\}(.*?)\{\{% */[a-zA-Z0-9_-]+ *%\}\}`)
	// A self-closing one, which on this site is always a widget on its own
	// line.
	soloShortcode = regexp.MustCompile(`(?m)^[ \t]*\{\{< *([a-zA-Z0-9_-]+)[^>]*>\}\}[ \t]*\n?`)
	// Anything the two above left behind, so an unhandled shape is caught
	// rather than published as literal braces.
	anyShortcode = regexp.MustCompile(`\{\{[<%]`)

	// Only the target, never the link text: a command signature in the
	// reference links under text like [`kx annotate <index> [<key=value>...]`],
	// whose own brackets nest deeper than a regular expression can follow.
	// What is rewritten is the part between "](" and ")" either way.
	markdownLink = regexp.MustCompile(`(\]\()([^)\s]+)\)`)

	blankRun = regexp.MustCompile(`\n{3,}`)
)

// dropped are the shortcodes that carry no prose: navigation listings and
// interactive widgets. A reader with only the text loses nothing by their
// going — the links a children listing would render are in llms.txt already,
// and a screenshot's alt text repeats what the paragraph beside it says.
var dropped = map[string]bool{
	"kx-children": true,
	"kx-shot":     true,
	"kx-terminal": true,
	"kx-themes":   true,
}

// Clean rewrites a page body for a reader who has only the text: shortcodes
// resolved or removed, and relative links made absolute against pageURL, since
// the body is read far from the page it came from.
//
// A shortcode this does not know is an error. The site's shortcodes are few
// and hand-written, and the alternative — passing it through — publishes
// literal template braces into a file an agent reads as prose.
func Clean(body, pageURL string) (string, error) {
	var bad error

	// Paired first: kx-note's body is prose, and dropping the wrapper without
	// keeping the inner text would lose a paragraph.
	body = replaceAll(pairedShortcode, body, func(groups []string) string {
		switch name := groups[1]; name {
		case "kx-note":
			return quote(groups[3], strings.Contains(groups[2], `kind="warn"`))
		default:
			bad = fmt.Errorf("unknown paired shortcode %q", name)
			return groups[0]
		}
	})

	body = replaceAll(soloShortcode, body, func(groups []string) string {
		if name := groups[1]; !dropped[name] {
			bad = fmt.Errorf("unknown shortcode %q", name)
			return groups[0]
		}
		return ""
	})

	if bad != nil {
		return "", bad
	}
	if loc := anyShortcode.FindStringIndex(body); loc != nil {
		return "", fmt.Errorf("a shortcode this generator does not handle, at byte %d", loc[0])
	}

	body, err := absolute(body, pageURL)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(blankRun.ReplaceAllString(body, "\n\n")) + "\n", nil
}

// quote renders an aside as a Markdown blockquote, which is the same aside in
// a file that has no stylesheet.
func quote(inner string, warn bool) string {
	label := "Note"
	if warn {
		label = "Warning"
	}

	lines := []string{"> **" + label + "**", ">"}
	for _, line := range strings.Split(strings.TrimSpace(inner), "\n") {
		if line = strings.TrimSpace(line); line == "" {
			lines = append(lines, ">")
			continue
		}
		lines = append(lines, "> "+line)
	}
	return strings.Join(lines, "\n") + "\n"
}

// absolute rewrites every relative Markdown link against the page's own URL.
// A link read out of its page is a link to nowhere: "../../getting-started/
// install/" resolves only against where it was written.
func absolute(body, pageURL string) (string, error) {
	page, err := url.Parse(pageURL)
	if err != nil {
		return "", fmt.Errorf("page url %q: %w", pageURL, err)
	}

	var bad error
	body = replaceAll(markdownLink, body, func(groups []string) string {
		target := groups[2]
		// A fragment stays as it is: it points within the page, and in
		// llms-full.txt that page's headings are right there.
		if strings.HasPrefix(target, "#") {
			return groups[0]
		}
		link, err := url.Parse(target)
		if err != nil {
			bad = fmt.Errorf("link %q: %w", target, err)
			return groups[0]
		}
		if link.IsAbs() {
			return groups[0]
		}
		return groups[1] + page.ResolveReference(link).String() + ")"
	})
	return body, bad
}

// replaceAll is ReplaceAllStringFunc with the submatches, which the standard
// library's version does not hand to its callback.
func replaceAll(re *regexp.Regexp, s string, fn func(groups []string) string) string {
	var out strings.Builder
	last := 0
	for _, loc := range re.FindAllStringSubmatchIndex(s, -1) {
		groups := make([]string, len(loc)/2)
		for i := range groups {
			if loc[2*i] >= 0 {
				groups[i] = s[loc[2*i]:loc[2*i+1]]
			}
		}
		out.WriteString(s[last:loc[0]])
		out.WriteString(fn(groups))
		last = loc[1]
	}
	out.WriteString(s[last:])
	return out.String()
}

// rootShort is kx's own one-line description, the same sentence `kx --help`
// opens with. Read from the command tree rather than written here, so the
// published summary cannot describe a kx that no longer exists.
func rootShort() string {
	return cli.NewRoot(cli.Services{}, "dev").Short
}

// section is one directory of pages, rendered as one H2 of llms.txt.
type section struct {
	// Chain is the weights from contentDir down to this directory, which is
	// the order the site's own navigation puts the sections in.
	Chain []int
	Title string
	Pages []Page
}

// sections groups pages by directory, each headed by that directory's own
// _index.md, in the order the site navigation shows them.
func sections(pages []Page) []section {
	indexes := map[string]Page{}
	grouped := map[string][]Page{}
	for _, page := range pages {
		if page.Index {
			indexes[page.Dir] = page
			continue
		}
		grouped[page.Dir] = append(grouped[page.Dir], page)
	}

	// The weight of each directory, for the chain below. A directory with no
	// _index.md sorts last within its parent rather than first, which a zero
	// would do.
	weight := func(dir string) int {
		if index, ok := indexes[dir]; ok {
			return index.Weight
		}
		return 1 << 30
	}

	var out []section
	for dir, group := range grouped {
		var chain []int
		for walk := dir; walk != ""; walk = parent(walk) {
			chain = append([]int{weight(walk)}, chain...)
		}

		title := indexes[dir].Title
		if title == "" {
			title = dir
		}

		sort.Slice(group, func(i, j int) bool {
			if group[i].Weight != group[j].Weight {
				return group[i].Weight < group[j].Weight
			}
			return group[i].Rel < group[j].Rel
		})
		out = append(out, section{Chain: chain, Title: title, Pages: group})
	}

	sort.Slice(out, func(i, j int) bool {
		if cmp := compare(out[i].Chain, out[j].Chain); cmp != 0 {
			return cmp < 0
		}
		return out[i].Title < out[j].Title
	})
	return out
}

func parent(dir string) string {
	if up := path.Dir(dir); up != "." {
		return up
	}
	return ""
}

func compare(a, b []int) int {
	for i := range a {
		if i >= len(b) {
			return 1
		}
		if a[i] != b[i] {
			if a[i] < b[i] {
				return -1
			}
			return 1
		}
	}
	if len(a) < len(b) {
		return -1
	}
	return 0
}

// Index renders llms.txt: the map, small enough for an agent to read whole and
// follow only the links it needs.
//
// The shape is the proposal's: an H1 naming the project, a blockquote summary,
// detail lines carrying no heading, then H2 sections of links. The summary is
// the root command's own Short, so the file describes kx the way kx does.
func Index(pages []Page, base string) string {
	var out strings.Builder
	fmt.Fprintf(&out, "# kx\n\n> %s\n\n", rootShort())
	fmt.Fprintf(&out, "kx is a kubectl wrapper. Running `kx get <resource>` lists resources and "+
		"numbers the rows; every other command takes those numbers in place of a name.\n\n")
	fmt.Fprintf(&out, "Every page below is also published as Markdown in one file at %sllms-full.txt, "+
		"which is the whole documentation and needs no further fetching.\n", base)

	for _, section := range sections(pages) {
		fmt.Fprintf(&out, "\n## %s\n\n", section.Title)
		for _, page := range section.Pages {
			fmt.Fprintf(&out, "- [%s](%s)", page.Name(), page.URL(base))
			if page.Description != "" {
				fmt.Fprintf(&out, ": %s", page.Description)
			}
			out.WriteString("\n")
		}
	}
	return out.String()
}

// Entry is one page of the embedded corpus, in the shape internal/docs reads.
// Deliberately not Page: what the binary carries is the rendered prose and
// where it came from, never the source path or the weights that ordered it.
type Entry struct {
	Title       string `json:"title"`
	Section     string `json:"section"`
	URL         string `json:"url"`
	Description string `json:"description"`
	Body        string `json:"body"`
}

// Corpus renders the documentation as the JSON internal/docs embeds, in the
// same order as llms-full.txt, so the file kx carries and the file the site
// publishes are the same documentation read two ways.
func Corpus(pages []Page, base string) (string, error) {
	sectionOf := map[string]string{}
	for _, section := range sections(pages) {
		for _, page := range section.Pages {
			sectionOf[page.Rel] = section.Title
		}
	}
	// A section's own page belongs to the section it heads, not to the one
	// above it — otherwise the four introductions file under nothing.
	for _, page := range pages {
		if page.Index {
			sectionOf[page.Rel] = page.Title
		}
	}

	entries := []Entry{}
	for _, page := range ordered(pages) {
		entries = append(entries, Entry{
			Title:       page.Title,
			Section:     sectionOf[page.Rel],
			URL:         page.URL(base),
			Description: page.Description,
			Body:        page.Body,
		})
	}

	// Indented, and a trailing newline, because it is a committed file a
	// reviewer reads a diff of.
	encoded, err := json.MarshalIndent(entries, "", "  ")
	if err != nil {
		return "", err
	}
	return string(encoded) + "\n", nil
}

// ordered is the pages in the order the site navigation shows them: the
// documentation's own front page, then each section's index ahead of the pages
// under it. Both Full and Corpus walk it, so the two never disagree about
// what comes after what.
func ordered(pages []Page) []Page {
	indexes := map[string]Page{}
	var out []Page
	for _, page := range pages {
		if !page.Index {
			continue
		}
		indexes[page.Dir] = page
		if page.Dir == "" {
			out = append(out, page)
		}
	}
	for _, section := range sections(pages) {
		if index, ok := indexes[section.Pages[0].Dir]; ok {
			out = append(out, index)
		}
		out = append(out, section.Pages...)
	}
	return out
}

// Full renders llms-full.txt: every page's prose, in the site's own order,
// each under its title and with the URL it came from.
//
// Section index pages are included like any other. Their navigation listing is
// gone with the shortcode that drew it, but the prose around it is written
// introduction and is worth as much here as on the page.
func Full(pages []Page, base string) string {
	var out strings.Builder
	fmt.Fprintf(&out, "# kx documentation\n\n> %s\n\n", rootShort())
	fmt.Fprintf(&out, "The whole of %sdocs/, as Markdown. Generated from the site's own sources by "+
		"tools/gen-docs-context; the map of it, with one link per page, is at %sllms.txt.\n",
		base, base)

	write := func(page Page) {
		fmt.Fprintf(&out, "\n---\n\n# %s\n\nSource: %s\n\n", page.Title, page.URL(base))
		if page.Description != "" {
			fmt.Fprintf(&out, "%s\n\n", page.Description)
		}
		out.WriteString(page.Body)
	}

	for _, page := range ordered(pages) {
		write(page)
	}
	return out.String()
}
