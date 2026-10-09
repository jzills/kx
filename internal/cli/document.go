package cli

import (
	"github.com/spf13/cobra"

	"github.com/jzills/kx/internal/render"
)

// documentAnnotation marks a command whose stdout is a document a program
// reads however it was invoked — a manifest, a log stream, whatever ran
// inside a container — rather than a table for a person. No flag reveals it:
// those commands produce one with none given, so machineOutput cannot infer
// it from the arguments the way it can for -o or --json.
//
// Two things read it. machineOutput keeps a refreshed listing off stdout for
// a stale index, and banner keeps the scope line off it too. One annotation
// rather than a list per behaviour, so the two cannot come to disagree about
// which commands write documents.
//
// kx cp does not carry it: kubectl cp writes files, and its stdout is its
// own progress, not the copy.
const documentAnnotation = "kx.document"

// documentAnnotations is the Command.Annotations value for a document
// command, and mutatingDocumentAnnotations for one that also spends its index
// on the cluster. Shared maps, for the reason mutatingAnnotations is one:
// nothing writes to a command's Annotations after construction.
var (
	documentAnnotations         = map[string]string{documentAnnotation: "true"}
	mutatingDocumentAnnotations = map[string]string{
		mutatingAnnotation: "true", documentAnnotation: "true",
	}
)

// writesDocument reports whether cmd's stdout carries a document rather than
// output for a person.
func writesDocument(cmd *cobra.Command) bool {
	return cmd != nil && cmd.Annotations[documentAnnotation] == "true"
}

// banner prints the context line naming what an index resolved to, above that
// resource's output — on stderr for a document command, where a person still
// reads it and `> pod.yaml` does not, and on stdout otherwise, where it
// belongs with the text it introduces.
func banner(cmd *cobra.Command, target Resolved, extra string) {
	if writesDocument(cmd) {
		render.BannerErr(string(target.Kind), target.Name, target.Namespace, extra)
		return
	}
	render.Banner(string(target.Kind), target.Name, target.Namespace, extra)
}
