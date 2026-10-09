package cli

import (
	"strings"
	"testing"

	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/fake"

	"github.com/jzills/kx/internal/state"
)

// Every command whose --match term empties its listing keeps the count label
// it uses when populated, and names the term in a row beneath — rather than
// putting the explanation where the count goes. See
// render.EmptyMatch for why.
func TestAnEmptyMatchKeepsItsCountLabel(t *testing.T) {
	const emptyTable = "NAME   READY   STATUS   RESTARTS   AGE"
	for _, tc := range []struct {
		name    string
		args    []string
		caption string
	}{
		{"get", []string{"get", "pods", "-n", "prod", "-m", "cron"},
			"Pods · prod · 0 items"},
		{"top", []string{"top", "-n", "prod", "-m", "cron"},
			"Pods · prod · 0 items"},
		{"diag", []string{"diag", "-n", "prod", "-m", "cron"},
			"Mixed · prod · 0 checked"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			stdout, _ := splitRender(t)
			kube := &recordingKubectl{output: emptyTable}
			services := staleServices(t, kube, &state.Query{Resource: "pods", Args: []string{}})
			services.Kubernetes = func() (kubernetes.Interface, error) {
				return fake.NewSimpleClientset(), nil
			}

			if err := Execute(NewRoot(services, "test"), tc.args); err != nil {
				t.Fatalf("kx %s: %v", strings.Join(tc.args, " "), err)
			}
			lines := strings.Split(strings.TrimRight(stdout.String(), "\n"), "\n")
			if len(lines) < 2 {
				t.Fatalf("got %d lines, want a caption and a row:\n%s", len(lines), stdout.String())
			}
			if lines[0] != tc.caption {
				t.Errorf("caption = %q, want %q", lines[0], tc.caption)
			}
			if !strings.Contains(lines[1], "nothing matches 'cron'") {
				t.Errorf("row = %q, want it to name the term", lines[1])
			}
		})
	}
}

// A listing emptied with no --match term is unchanged: there is no term to
// put in a row, so the caption keeps saying "none found". This is the control
// that stops the change above from being applied to every empty listing.
func TestAnEmptyListingWithoutAMatchStillSaysNoneFound(t *testing.T) {
	stdout, _ := splitRender(t)
	kube := &recordingKubectl{output: "NAME   READY   STATUS   RESTARTS   AGE"}
	services := staleServices(t, kube, &state.Query{Resource: "pods", Args: []string{}})

	if err := Execute(NewRoot(services, "test"), []string{"get", "pods", "-n", "prod"}); err != nil {
		t.Fatalf("kx get pods: %v", err)
	}
	// First line only: an empty listing is also followed by the back-hint,
	// which this change does not touch.
	caption := strings.SplitN(stdout.String(), "\n", 2)[0]
	if caption != "Pods · prod · none found" {
		t.Errorf("caption = %q, want the unchanged none-found caption", caption)
	}
	if strings.Contains(stdout.String(), "0 items") {
		t.Errorf("stdout = %q, want no zero count where there is no term to explain", stdout.String())
	}
}
