// Command voicecheck enforces the parts of STYLE.md a machine can check, so a
// contributor without an agent is held to them as well as one with:
//
//	go run ./tools/voicecheck
//
// It reads every tracked file and reports each finding as file:line, the
// problem, and the STYLE.md section that states the rule, exiting 1 when there
// is any. Run by a pre-commit hook, and so by CI's pre-commit job.
//
// The rules are the mechanical ones only: American spelling in Go comments and
// strings and in Markdown prose, no label prefix on a message, no README or
// site sentence that opens with inline code or a lowercase "kx", and no
// hard-wrapped release-notes paragraph. Everything else in STYLE.md is
// judgment, which is what review is for.
package main

import (
	"bufio"
	"bytes"
	"fmt"
	"go/scanner"
	"go/token"
	"os"
	"os/exec"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// Finding is one breach of a rule, located well enough to jump to.
type Finding struct {
	File    string
	Line    int
	Message string
	Section string
}

func (f Finding) String() string {
	return fmt.Sprintf("%s:%d: %s (STYLE.md: %s)", f.File, f.Line, f.Message, f.Section)
}

func main() {
	files, err := trackedFiles()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	var findings []Finding
	for _, file := range files {
		content, err := os.ReadFile(file)
		if err != nil {
			// A tracked file deleted in the working tree has nothing to check.
			continue
		}
		findings = append(findings, Check(file, content)...)
	}
	for _, finding := range findings {
		fmt.Println(finding)
	}
	if len(findings) > 0 {
		os.Exit(1)
	}
}

func trackedFiles() ([]string, error) {
	// -z: NUL-separated and unquoted, so a path with a space or a non-ASCII
	// character is read whole rather than split, or read in git's quoting,
	// and skipped as unreadable.
	out, err := exec.Command("git", "ls-files", "-z").Output()
	if err != nil {
		return nil, fmt.Errorf("git ls-files: %w", err)
	}
	return strings.FieldsFunc(string(out), func(r rune) bool { return r == 0 }), nil
}

// skipped are paths kx ships but did not write — vendored libraries keep the
// spelling and style their authors gave them — and this tool, whose word list
// and tests are made of the spellings it rejects.
var skipped = regexp.MustCompile(`(^|/)vendor/|^site/_vendor/|^tools/voicecheck/`)

// Check returns every finding in one file, choosing the rules by where the
// file lives and what it is.
func Check(path string, content []byte) []Finding {
	if skipped.MatchString(path) {
		return nil
	}
	var findings []Finding
	switch {
	case strings.HasSuffix(path, ".go"):
		findings = append(findings, checkGo(path, content)...)
	case strings.HasSuffix(path, ".md"):
		findings = append(findings, checkMarkdown(path, content)...)
	}
	sort.SliceStable(findings, func(i, j int) bool { return findings[i].Line < findings[j].Line })
	return findings
}

// british maps each British spelling kx has used to its American one. Listed
// rather than derived from a suffix rule, because "-ise" also ends promise,
// exercise and otherwise. A spelling both varieties share is left out:
// "analyses" is the British verb, and also everyone's plural of analysis.
var british = map[string]string{
	"analyse": "analyze", "analysed": "analyzed", "analogue": "analog",
	"behaviour": "behavior", "behaviours": "behaviors", "behavioural": "behavioral",
	"cancelled": "canceled", "cancelling": "canceling",
	"catalogue": "catalog", "categorised": "categorized",
	"centre": "center", "centred": "centered", "centres": "centers",
	"colour": "color", "colours": "colors", "coloured": "colored", "colouring": "coloring",
	"generalise": "generalize", "grey": "gray",
	"honour": "honor", "honours": "honors", "honoured": "honored", "honouring": "honoring",
	"initialise": "initialize", "initialised": "initialized", "judgement": "judgment",
	"labelled": "labeled", "labelling": "labeling", "labour": "labor", "licence": "license",
	"memoise": "memoize", "memoised": "memoized", "memoises": "memoizes", "mislabelled": "mislabeled",
	"neighbour": "neighbor", "neighbours": "neighbors", "neighbouring": "neighboring",
	"neutralise": "neutralize", "neutralised": "neutralized", "neutralises": "neutralizes",
	"neutralising": "neutralizing", "neutralisation": "neutralization",
	"normalise": "normalize", "normalised": "normalized", "normalises": "normalizes",
	"normalisation": "normalization",
	"organise":      "organize", "organised": "organized", "pluralise": "pluralize", "pluralising": "pluralizing",
	"prioritise": "prioritize", "randomise": "randomize", "randomised": "randomized",
	"randomises": "randomizes", "rasterise": "rasterize", "rasterises": "rasterizes",
	"realise": "realize", "realised": "realized",
	"recognise": "recognize", "recognised": "recognized", "recognises": "recognizes",
	"serialise": "serialize", "serialised": "serialized", "serialiser": "serializer",
	"serialises": "serializes", "serialising": "serializing",
	"signalled": "signaled", "standardise": "standardize", "standardises": "standardizes",
	"summarise": "summarize", "unlabelled": "unlabeled", "unrecognised": "unrecognized",
	"utilise": "utilize",
}

// protocol is spelling kx does not own: names another system defines, which
// have to be written its way.
var protocol = []string{"notifications/cancelled"}

var (
	word      = regexp.MustCompile(`[A-Za-z]+`)
	camelPart = regexp.MustCompile(`[A-Z]?[a-z]+|[A-Z]+`)
)

// spelling reports the British words in text, which starts at line in path.
// Identifiers are split at case changes, so a test named
// TestLastLineStripsScannerColour is caught the same as the bare word.
func spelling(path string, line int, text string) []Finding {
	for _, name := range protocol {
		text = strings.ReplaceAll(text, name, "")
	}
	var findings []Finding
	for i, row := range strings.Split(text, "\n") {
		for _, w := range word.FindAllString(row, -1) {
			for _, part := range camelPart.FindAllString(w, -1) {
				if american, ok := british[strings.ToLower(part)]; ok {
					findings = append(findings, Finding{
						File: path, Line: line + i,
						Message: fmt.Sprintf("British spelling '%s' — write '%s'", part, american),
						Section: "The voice",
					})
				}
			}
		}
	}
	return findings
}

// labelPrefix is a message that announces its own kind ahead of the sentence.
// The glyph or the muted style already says it.
var labelPrefix = regexp.MustCompile(`(?i)^(note|warning|info|error|kx):\s`)

// messagePackages hold the strings kx prints; a label prefix is checked only
// there, since elsewhere a "warning:" literal is usually someone else's text
// being matched rather than kx's being written.
var messagePackages = regexp.MustCompile(`^internal/(cli|render)/`)

func checkGo(path string, content []byte) []Finding {
	fset := token.NewFileSet()
	file := fset.AddFile(path, -1, len(content))
	var s scanner.Scanner
	s.Init(file, content, nil, scanner.ScanComments)
	messages := messagePackages.MatchString(path) && !strings.HasSuffix(path, "_test.go")

	var findings []Finding
	for {
		pos, tok, lit := s.Scan()
		if tok == token.EOF {
			break
		}
		if tok != token.COMMENT && tok != token.STRING && tok != token.CHAR {
			continue
		}
		line := fset.Position(pos).Line
		findings = append(findings, spelling(path, line, lit)...)
		if messages && tok == token.STRING {
			if value, err := strconv.Unquote(lit); err == nil && labelPrefix.MatchString(value) {
				findings = append(findings, Finding{
					File: path, Line: line,
					Message: fmt.Sprintf("message opens with a label (%q) — drop it; the glyph says what kind of message it is",
						labelPrefix.FindString(value)),
					Section: "The voice",
				})
			}
		}
	}
	return findings
}

// pitchDocs are the Markdown files written one sentence per line, where a
// line's first word is a sentence's first word. The command reference under
// site/content/docs/reference/commands is generated from help strings, which
// follow the Help text rules instead.
var pitchDocs = regexp.MustCompile(`^(README\.md|site/content/docs/.*\.md)$`)

var generatedDocs = regexp.MustCompile(`^site/content/docs/reference/commands/`)

var listMarker = regexp.MustCompile(`^\s*([-*+]|\d+\.)\s+`)

func checkMarkdown(path string, content []byte) []Finding {
	opensSentences := pitchDocs.MatchString(path) && !generatedDocs.MatchString(path)
	releaseNotes := strings.HasPrefix(path, "release-notes/")

	var findings []Finding
	inFence, inFrontMatter := false, false
	previousProse := false
	lines := bufio.NewScanner(bytes.NewReader(content))
	lines.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for n := 1; lines.Scan(); n++ {
		line := lines.Text()
		trimmed := strings.TrimSpace(line)
		switch {
		case n == 1 && trimmed == "---":
			inFrontMatter = true
			continue
		case inFrontMatter:
			if trimmed == "---" {
				inFrontMatter = false
			}
			continue
		case strings.HasPrefix(trimmed, "```"):
			inFence = !inFence
			previousProse = false
			continue
		case inFence:
			continue
		}

		findings = append(findings, spelling(path, n, line)...)

		prose := trimmed != "" && !strings.ContainsAny(trimmed[:1], "|#<>!")
		// A list item starts a line of its own; a line after one that is
		// not an item is the item wrapped.
		if releaseNotes && prose && previousProse && !listMarker.MatchString(line) {
			findings = append(findings, Finding{
				File: path, Line: n,
				Message: "paragraph is hard-wrapped — keep each paragraph on one line",
				Section: "Release notes",
			})
		}
		previousProse = prose

		if !opensSentences || !prose {
			continue
		}
		sentence := listMarker.ReplaceAllString(line, "")
		sentence = strings.TrimLeft(sentence, " ")
		switch {
		case strings.HasPrefix(sentence, "`"):
			findings = append(findings, Finding{
				File: path, Line: n,
				Message: "sentence opens with inline code — name what it is first (\"The `--x` flag…\") or reword",
				Section: "README and site docs",
			})
		case strings.HasPrefix(sentence, "kx "):
			findings = append(findings, Finding{
				File: path, Line: n,
				Message: "sentence opens with a lowercase \"kx\" — reword so kx is not first",
				Section: "README and site docs",
			})
		}
	}
	return findings
}
