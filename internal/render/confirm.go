package render

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/jzills/kx/internal/theme"
)

// ErrAborted is returned when the user declines a confirmation prompt. It
// carries no message: the prompt already told them what happened, and printing
// "aborted" underneath their own "n" is noise.
type ErrAborted struct{}

func (ErrAborted) Error() string { return "aborted" }

// Confirm asks a yes/no question, defaulting to no. Anything other than an
// explicit yes aborts, so a stray newline never deletes a resource.
//
// kind/name tokens are accented to match banners.
func (r *Renderer) Confirm(message string) error {
	return r.confirmFrom(os.Stdin, message)
}

// prompts buffers the answer stream once and reuses it for every later prompt.
//
// bufio reads ahead past the newline it returns, so wrapping the stream afresh
// for each prompt strands the remaining answers in the discarded buffer: piped
// input to `kx delete 1 2` would delete the first resource and abort on the
// second. One renderer reads from one source, so caching it here is enough.
func (r *Renderer) prompts(in io.Reader) *bufio.Reader {
	if r.answers == nil {
		r.answers = bufio.NewReader(in)
	}
	return r.answers
}

func (r *Renderer) confirmFrom(in io.Reader, message string) error {
	fmt.Fprint(r.out, r.emphasizePaths(message)+" "+r.style(theme.Muted, "[y/n] (n):")+" ")

	answer, err := r.prompts(in).ReadString('\n')
	if err != nil && answer == "" {
		// EOF (a closed or empty stdin) is not consent.
		fmt.Fprintln(r.out)
		return ErrAborted{}
	}
	switch strings.ToLower(strings.TrimSpace(answer)) {
	case "y", "yes":
		return nil
	default:
		return ErrAborted{}
	}
}

// Confirm prompts through the package-level renderer.
func Confirm(message string) error { return current.Confirm(message) }
