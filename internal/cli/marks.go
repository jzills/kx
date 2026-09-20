// Named marks: pinning a resource to a chosen name so it survives the
// re-listings that move every index. Storage and resolution live in
// internal/state; this file is where a mark is created and removed.
package cli

import (
	"fmt"
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
		Use:        "mark [name] [index]",
		SuggestFor: []string{"marks"},
		Short:      "Pin an indexed resource to a name that survives re-listing; with no arguments, lists marks.",
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

// newUnmarkCommand removes one mark by name, or every mark with --all.
func newUnmarkCommand(services Services) *cobra.Command {
	var all bool
	cmd := &cobra.Command{
		Use:        "unmark [name]",
		SuggestFor: []string{"unmarks"},
		Short:      "Remove a mark by name; --all removes every mark.",
		Long: "Removes a mark by name, or every mark at once with --all — the marks 'kx state " +
			"drop --all' deliberately leaves behind.",
		Example: "  kx unmark api\n  kx unmark --all",
		Args:    cobra.MaximumNArgs(1),
		Annotations: map[string]string{
			"arg.name": "Mark name to remove",
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
			if len(args) != 1 {
				return fmt.Errorf("kx unmark requires a name, or --all to remove every mark")
			}
			// The mark listing prints names with their sigil ("@api"), and
			// copying what is on screen is the obvious way to spend one — so a
			// leading '@' is accepted rather than reported as an unknown mark
			// named "@api".
			name := strings.TrimPrefix(args[0], "@")
			if err := services.State.DropMark(name); err != nil {
				return err
			}
			render.Success(fmt.Sprintf("Removed mark @%s", name))
			return nil
		},
	}
	cmd.Flags().BoolVar(&all, "all", false, "Remove every mark")
	return cmd
}
