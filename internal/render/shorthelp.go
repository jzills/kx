package render

import "strings"

// shortHelpLimit matches the limit the Python CLI passes to Click's
// get_short_help_str, so the root help truncates at the same point.
const shortHelpLimit = 55

// shortHelp condenses a command description for the root help listing,
// reproducing Click's make_default_short_help.
//
// Truncation is by word and by character budget, not by terminal width: the
// same description reads the same on any terminal and in a pipe. A first
// sentence that ends within the budget is returned whole, with no ellipsis —
// which is why the shorter descriptions appear in full.
func shortHelp(text string, limit int) string {
	words := strings.Fields(text)
	if len(words) == 0 {
		return ""
	}

	total := 0
	cut := -1
	for i, word := range words {
		total += len(word)
		if i > 0 {
			total++ // the space before this word
		}
		if total > limit {
			cut = i
			break
		}
		if strings.HasSuffix(word, ".") {
			// A complete sentence inside the budget needs no ellipsis.
			return strings.Join(words[:i+1], " ")
		}
		if total == limit && i != len(words)-1 {
			cut = i
			break
		}
	}
	if cut < 0 {
		return strings.Join(words, " ")
	}

	// Drop words until the text plus the ellipsis fits.
	total += len("...")
	for cut > 0 {
		total -= len(words[cut])
		if cut > 0 {
			total--
		}
		if total <= limit {
			break
		}
		cut--
	}
	return strings.Join(words[:cut], " ") + "..."
}
