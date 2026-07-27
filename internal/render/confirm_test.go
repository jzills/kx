package render

import (
	"bytes"
	"errors"
	"strings"
	"testing"
)

func confirmWith(t *testing.T, answer string) (error, string) {
	t.Helper()
	var buf bytes.Buffer
	renderer := New(&buf, &buf, "github-dark", false)
	err := renderer.confirmFrom(strings.NewReader(answer), "Delete Pod/nginx in prod?")
	return err, buf.String()
}

func TestConfirmAcceptsYes(t *testing.T) {
	for _, answer := range []string{"y\n", "Y\n", "yes\n", "YES\n", " y \n"} {
		if err, _ := confirmWith(t, answer); err != nil {
			t.Errorf("answer %q was rejected: %v", answer, err)
		}
	}
}

// Anything other than an explicit yes aborts, so a stray newline never deletes
// a resource.
func TestConfirmDefaultsToNo(t *testing.T) {
	for _, answer := range []string{"\n", "n\n", "no\n", "maybe\n", "yolo\n"} {
		err, _ := confirmWith(t, answer)
		if err == nil {
			t.Errorf("answer %q was accepted, want abort", answer)
		}
	}
}

// A closed or empty stdin is not consent — this is the piped-input case, where
// there is nobody to ask.
func TestConfirmTreatsEOFAsNo(t *testing.T) {
	err, _ := confirmWith(t, "")
	if err == nil {
		t.Error("EOF was accepted as consent")
	}
	var aborted ErrAborted
	if !errors.As(err, &aborted) {
		t.Errorf("err = %v, want ErrAborted", err)
	}
}

func TestConfirmShowsTheQuestionAndDefault(t *testing.T) {
	_, out := confirmWith(t, "y\n")
	if !strings.Contains(out, "Delete Pod/nginx in prod?") {
		t.Errorf("prompt = %q, want the question", out)
	}
	if !strings.Contains(out, "[y/n] (n):") {
		t.Errorf("prompt = %q, want the default shown as n", out)
	}
}
