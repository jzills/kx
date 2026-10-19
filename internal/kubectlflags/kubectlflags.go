// Package kubectlflags is kubectl's own flag definitions for the subcommands kx
// forwards to, read out of kubectl's source rather than written down by hand.
//
// kx splits a forwarding command's argv itself, and to do that correctly it has
// to know which flags take a value: in `kx get pods -A 1` the 1 is an index,
// because -A takes none, while in `kx get pods -n 5` the 5 is a namespace. That
// is not recoverable from the shape of the arguments, so it comes from here.
//
// The table in table_gen.go is written by tools/gen-kubectl-flags, which builds
// kubectl's command tree from k8s.io/kubectl and copies each flag's definition
// out of it. That module is the generator's alone; kx itself never links
// kubectl's command code, only the few hundred lines of data it produced.
package kubectlflags

import "slices"

// Flag is one kubectl flag, as pflag defines it.
type Flag struct {
	// Name is the long name, without the leading dashes.
	Name string
	// Shorthand is the one-letter name, empty when there is none.
	Shorthand string
	// ShorthandDeprecated is kubectl's message for using the shorthand, empty
	// when the shorthand is not deprecated.
	ShorthandDeprecated string
	// Type is pflag's name for the value's type: "bool", "string",
	// "stringArray", "duration" and so on.
	Type string
	// NoOptDefVal is the value the flag takes when given with none. A flag that
	// has one never consumes the next argument; a value can only be attached
	// with "=", as in --dry-run=client.
	NoOptDefVal string
	// Usage is kubectl's help text for the flag.
	Usage string
	// Hidden flags work but are left out of kubectl's help.
	Hidden bool
	// Deprecated is kubectl's deprecation message, empty when the flag is not.
	// A deprecated flag still works.
	Deprecated string
	// Filename is set on a flag kubectl marks as taking a file (-f, --template),
	// the one kind of value shell completion should offer files for.
	Filename bool
	// Extensions narrows a Filename flag's files to these extensions, empty
	// for any file.
	Extensions []string
}

// TakesValue reports whether the flag consumes the next argument when written
// without "=". This is pflag's own rule: a flag with no NoOptDefVal requires a
// value, so `-n x` reads x as the namespace, while `-A x` leaves x alone.
func (f Flag) TakesValue() bool { return f.NoOptDefVal == "" }

// Command returns the flags kubectl defines on a subcommand, by its path below
// kubectl: "get", "rollout restart", "top pod". The global flags every
// subcommand inherits (see Global) are not included. A flag here can share a
// global flag's name, and then it is the one that applies: kubectl debug's
// --profile is a debugging profile, not the global pprof one. ok is false for
// a subcommand the table was not generated for.
func Command(path string) (flags []Flag, ok bool) {
	flags, ok = commands[path]
	return slices.Clone(flags), ok
}

// Commands returns the subcommand paths the table covers, sorted.
func Commands() []string {
	paths := make([]string, 0, len(commands))
	for path := range commands {
		paths = append(paths, path)
	}
	slices.Sort(paths)
	return paths
}

// Global returns the flags kubectl defines on its root command, which every
// subcommand accepts: --namespace, --context, --kubeconfig and the rest.
func Global() []Flag { return slices.Clone(global) }
