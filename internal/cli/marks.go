// Named marks: pinning a resource to a chosen name so it survives the
// re-listings that move every index. Storage and resolution live in
// internal/state; this file is where a mark is created and removed.
package cli

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/jzills/kx/internal/render"
	"github.com/jzills/kx/internal/state"
	"github.com/spf13/cobra"
)

// newMarkCommand pins a resource to a name, or — with no arguments — lists
// the marks already set.
//
// The index is resolved through resolveRefs before SaveMark is ever called,
// so a bad index refuses without leaving a half-made mark behind. Listing
// saves no state: a mark is spent by name, not by position, so numbering the
// list would invite `kx mark 2` to mean something it doesn't.
func newMarkCommand(services Services) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "mark [name] [index]",
		Short: "Pin an indexed resource to a name that survives re-listing; with no arguments, lists marks.",
		Long: "Pins whatever `<index>` currently resolves to under `<name>`, so 'kx logs @name' keeps " +
			"working after a later listing moves every index around it.\n\n" +
			"With no arguments, lists the marks that are set. That listing carries no index " +
			"column and saves no state — a mark is spent by the name it was given, never by " +
			"position.",
		Example: "  kx mark\n  kx mark api 3",
		Args: func(cmd *cobra.Command, args []string) error {
			if len(args) == 0 || len(args) == 2 {
				return nil
			}
			return fmt.Errorf(
				"kx mark takes a name and an index, or no arguments to list marks — see 'kx mark --help' for usage.")
		},
		Annotations: map[string]string{
			"arg.name": "Name to give the mark",
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) == 0 {
				return listMarks(services)
			}
			name, indexArg := args[0], args[1]
			if err := validMarkName(name); err != nil {
				return err
			}
			resolved, err := resolveRefs(services.State, "index", []string{indexArg})
			if err != nil {
				return err
			}
			resource := resolved[0]
			if err := services.State.SaveMark(name, state.Mark{
				Resource: state.Resource{
					Name: resource.Name, Kind: resource.Kind, Namespace: resource.Namespace,
				},
				Context: services.Kubectl.CurrentContext(),
			}); err != nil {
				return err
			}
			render.Success(fmt.Sprintf("Marked @%s → %s/%s", name, resource.Kind, resource.Name))
			return nil
		},
	}
	return cmd
}

// listMarks fetches the marks currently set and hands them to render.MarkList
// — the presentation, including the empty-state message and the sort order,
// lives in internal/render beside every other listing renderer.
func listMarks(services Services) error {
	marks, err := services.State.Marks()
	if err != nil {
		return err
	}
	render.MarkList(marks)
	return nil
}

// newUnmarkCommand removes marks by name, or every mark with --all.
//
// Names, never positions. Every other listing in kx numbers its rows because
// they have no durable handle; a mark's whole purpose is that it has one, and
// it is already the leftmost column. Numbering them would also put a row
// number beside a Pod on a resource-shaped listing, which invites being spent
// at the next command — where it would resolve against the current listing
// instead, silently and against something else.
func newUnmarkCommand(services Services) *cobra.Command {
	var all bool
	cmd := &cobra.Command{
		Use:   "unmark [name...]",
		Short: "Remove marks by name; --all removes every mark.",
		Long: "Removes one or more marks by name, or every mark at once with --all — the marks " +
			"'kx state drop --all' deliberately leaves behind.\n\nNames rather than row numbers: " +
			"the mark listing carries no index column, because a mark is spent by the name it " +
			"was given. A name that is not a mark refuses the whole call, so a typo partway " +
			"through leaves every mark in place.",
		Example: "  kx unmark api\n  kx unmark api web db\n  kx unmark --all",
		Args:    cobra.ArbitraryArgs,
		Annotations: map[string]string{
			"arg.name": "Mark names to remove",
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			if all {
				if len(args) > 0 {
					return fmt.Errorf("kx unmark --all takes no name argument")
				}
				if err := services.confirm()(
					"Remove every mark? Listings and slots are untouched.",
				); err != nil {
					return err
				}
				if err := services.State.DropAllMarks(); err != nil {
					return err
				}
				render.Success("Removed all marks.")
				return nil
			}
			if len(args) == 0 {
				return fmt.Errorf("kx unmark requires a name, or --all to remove every mark")
			}
			names, err := markNames(args)
			if err != nil {
				return err
			}
			if err := services.State.DropMarks(names); err != nil {
				return err
			}
			render.Success(removedMarks(names))
			return nil
		},
	}
	cmd.Flags().BoolVar(&all, "all", false, "Remove every mark")
	return cmd
}

// markNames turns the arguments into the mark names to remove, deduped in the
// order they were written.
//
// The mark listing prints names with their sigil ("@api"), and copying what is
// on screen is the obvious way to spend one — so a leading '@' is accepted
// rather than reported as an unknown mark named "@api". It is accepted on any
// argument, not only the first: a list copied off the screen carries it on
// every one, and a list typed from memory on none.
//
// Each name is validated the same way kx mark validates one before ever
// creating it: DropMarks' "No mark named" error interpolates whatever it is
// handed straight into its "run 'kx mark %s <index>'" suggestion, so an empty
// name (a bare "@") or one that still carries a sigil (a doubled "@@web")
// turned that suggestion into a command that cannot work — 'kx mark  <index>'
// with a name-shaped hole, or 'kx mark @web <index>', which validMarkName
// itself rejects. Catching it here reports the bad name plainly instead of
// recommending either.
//
// A name written twice is one mark, the way a repeated index is one resource
// (see parseRefs): the repeat is dropped rather than refused, because the
// second removal would report the mark as unknown — an error about the user's
// own success.
func markNames(args []string) ([]string, error) {
	names := make([]string, 0, len(args))
	seen := make(map[string]bool, len(args))
	for _, arg := range args {
		if err := notAPosition(arg); err != nil {
			return nil, err
		}
		name := strings.TrimPrefix(arg, "@")
		if err := validMarkName(name); err != nil {
			return nil, err
		}
		if seen[name] {
			continue
		}
		seen[name] = true
		names = append(names, name)
	}
	return names, nil
}

// notAPosition refuses the row numbers the rest of kx is spent with, since
// this is the one listing that has none.
//
// Caught here rather than left to the name validators, which answer the
// question they were written for and not this one: a bare number came back as
// "'1' is a number, which a mark name cannot be", which is about creating a
// mark, and a range came back as "No mark named '1..3'" — "1..3" is dots and
// digits, both legal in a name — which reads as a typo rather than as a
// spelling kx does not have.
func notAPosition(arg string) error {
	spelling := strings.TrimPrefix(arg, "@")
	_, numeric := strconv.Atoi(spelling)
	if numeric != nil && !strings.Contains(spelling, "..") {
		return nil
	}
	return fmt.Errorf(
		"kx unmark takes mark names, not row numbers — the mark listing has no index "+
			"column, because a mark is spent by the name it was given. Run 'kx mark' to "+
			"see the names, then 'kx unmark <name>'. Got '%s'.", arg)
}

// removedMarks reports what was removed, naming every mark with the sigil the
// listing prints rather than counting them — the names are what the user
// typed, and a count of them says nothing the line above it hasn't.
func removedMarks(names []string) string {
	spelled := make([]string, len(names))
	for i, name := range names {
		spelled[i] = "@" + name
	}
	if len(spelled) == 1 {
		return "Removed mark " + spelled[0]
	}
	return "Removed marks " + strings.Join(spelled, ", ")
}
