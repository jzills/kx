package render

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"strings"
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

func (r *Renderer) confirmFrom(in io.Reader, message string) error {
	fmt.Fprint(r.out, r.emphasizePaths(message)+" "+r.style("muted", "[y/n] (n):")+" ")

	reader := bufio.NewReader(in)
	answer, err := reader.ReadString('\n')
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
