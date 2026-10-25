package cli

import (
	"strings"
	"testing"

	"github.com/jzills/kx/internal/config"
	"github.com/jzills/kx/internal/kinds"
	"github.com/jzills/kx/internal/state"
	"github.com/spf13/cobra"
)

// A command whose stdout is a document keeps everything of its own off it.
//
// kx yaml and kx logs printed their scope banner to stdout, above the
// document: `kx yaml 1 > pod.yaml` wrote "Pod/nginx · prod" as the file's
// first line, so the file was not YAML and `| yq` could not parse it. The
// banner is worth keeping — it says which resource an index resolved to —
// it just belongs on stderr, where a person still reads it and a program
// does not. Same reasoning as documentAnnotation itself, which is what
// decides this.
func TestADocumentCommandKeepsItsBannerOffStdout(t *testing.T) {
	const manifest = "apiVersion: v1\nkind: Pod\nmetadata:\n  name: api-old\n"
	for _, tc := range []struct {
		name string
		args []string
	}{
		{"yaml", []string{"yaml", "1"}},
		{"logs", []string{"logs", "1"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			stdout, stderr := splitRender(t)
			kube := &recordingKubectl{output: manifest}
			services := staleServices(t, kube, &state.Query{Resource: "pods", Args: []string{}})

			if err := Execute(NewRoot(services, "test"), tc.args); err != nil {
				t.Fatalf("kx %s: %v", strings.Join(tc.args, " "), err)
			}
			if strings.Contains(stdout.String(), "Pod/api-old") {
				t.Errorf("the banner reached stdout, where the document belongs:\n%s",
					stdout.String())
			}
			if !strings.Contains(stderr.String(), "Pod/api-old") {
				t.Errorf("stderr = %q, want the banner naming what the index resolved to",
					stderr.String())
			}
		})
	}
}

// kx describe is not a document command: its output is for a person either
// way, so its banner stays on stdout with the text it introduces. The
// annotation is the whole of the difference, so this is what stops the fix
// above from being applied to every banner in the tree.
func TestDescribeKeepsItsBannerWithItsOutput(t *testing.T) {
	stdout, _ := splitRender(t)
	kube := &recordingKubectl{output: "Name: api-old\n"}
	services := staleServices(t, kube, &state.Query{Resource: "pods", Args: []string{}})

	if err := Execute(NewRoot(services, "test"), []string{"describe", "1"}); err != nil {
		t.Fatalf("kx describe 1: %v", err)
	}
	if !strings.Contains(stdout.String(), "Pod/api-old") {
		t.Errorf("stdout = %q, want describe's banner above its output", stdout.String())
	}
}

// Several manifests are one YAML stream, so they are separated by YAML's own
// "---" and not by a blank line. With a blank line the concatenation parsed
// as a single document with every key repeated, which no reader accepts — so
// `kx yaml 1 2 > pods.yaml` was unusable even once the banners moved.
func TestSeveralManifestsAreSeparatedByADocumentMarker(t *testing.T) {
	stdout, _ := splitRender(t)
	kube := &recordingKubectl{output: "apiVersion: v1\nkind: Pod\n"}
	services := staleServices(t, kube, &state.Query{Resource: "pods", Args: []string{}})
	if err := services.State.Save(state.State{
		Resources: state.NewResources([]string{"api-old", "api-new"}, kinds.Pod),
		Namespace: "prod",
		Query:     &state.Query{Resource: "pods", Args: []string{}},
	}); err != nil {
		t.Fatalf("seed state: %v", err)
	}

	if err := Execute(NewRoot(services, "test"), []string{"yaml", "1", "2"}); err != nil {
		t.Fatalf("kx yaml 1 2: %v", err)
	}
	if got := strings.Count(stdout.String(), "\n---\n"); got != 1 {
		t.Errorf("found %d document separators between two manifests, want 1:\n%s",
			got, stdout.String())
	}
}

// The pinning test for documentAnnotation, shaped like the one for
// kx.mutating: every command in the tree is on one of two lists, and a command
// on neither fails, so a new one has to decide rather than land on the
// unannotated side by default. The annotation is what machineOutput reads to
// keep a refreshed listing off stdout, and what banner reads to move its line
// to stderr; a document command that forgot it would print a table or a banner
// into the file a program is reading, and no flag reveals that from outside.
//
// Subcommands are walked too, named by their path ("set image"), so a
// document subcommand is pinned the same way. kx ref and the completion
// scripts are read by programs but stay off the list: neither resolves an
// index through withRefresh, so neither has a listing or a banner to misplace.
func TestDocumentAnnotationAllowlistIsPinned(t *testing.T) {
	document := map[string]bool{
		// docs prints a documentation page: always a document, whoever is
		// reading, and nothing kx would paste a listing into.
		"debug": true, "docs": true, "exec": true, "logs": true, "yaml": true,
	}
	other := map[string]bool{
		"annotate": true, "annotations": true, "completion": true,
		"completion bash": true, "completion fish": true,
		"completion powershell": true, "completion zsh": true,
		"context": true, "cordon": true, "cp": true, "delete": true,
		"describe": true, "diagnostic": true, "drain": true, "edit": true,
		"engine": true, "events": true, "get": true, "label": true,
		"labels": true, "mark": true, "mcp": true, "namespace": true,
		"port-forward": true, "ref": true, "rollout": true, "scale": true,
		"scan": true, "secret": true, "set": true, "set image": true,
		"state": true, "state back": true, "state drop": true,
		"state forward": true, "theme": true, "top": true, "tree": true,
		"uncordon": true, "unmark": true, "wait": true,
	}
	root := NewRoot(NewServices(config.Default()), "test")
	seen := map[string]bool{}
	var walk func(cmd *cobra.Command)
	walk = func(cmd *cobra.Command) {
		for _, child := range cmd.Commands() {
			name := strings.TrimPrefix(child.CommandPath(), root.Name()+" ")
			seen[name] = true
			switch want, got := document[name], writesDocument(child); {
			case want && !got:
				t.Errorf("%s is on the document list but carries no %s annotation",
					name, documentAnnotation)
			case !want && got:
				t.Errorf("%s carries %s but is not on the document list — "+
					"was it made a document command without updating the pin?",
					name, documentAnnotation)
			case !want && !other[name]:
				t.Errorf("%s is a command neither list accounts for — decide whether "+
					"its stdout is a document a program reads, then add it to one list", name)
			}
			walk(child)
		}
	}
	walk(root)
	for name := range document {
		if !seen[name] {
			t.Errorf("%s is on the document list but no longer exists as a command", name)
		}
	}
}
