package cli

import (
	"errors"
	"fmt"
	"regexp"
	"strings"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

// Cobra and pflag write their own refusals — "unknown command", "unknown
// flag", "accepts at most 1 arg(s)" — in a register kx doesn't use anywhere
// else: lowercase, no period, and nothing about what to do. The helpers here
// put each one into the sentence kx would have written, so a typo reads the
// same as every refusal kx raises itself.

// maxArgs is minArgs' counterpart: at most n positional arguments, refused in
// kx's voice rather than cobra's "accepts at most N arg(s), received M". It
// names the first argument past the limit, the way refuseStray does for a
// forwarding command.
func maxArgs(n int) cobra.PositionalArgs {
	return func(cmd *cobra.Command, args []string) error {
		if len(args) <= n {
			return nil
		}
		path := cmd.CommandPath()
		return fmt.Errorf("'%s' isn't an argument of %s — see '%s --help' for usage.",
			args[n], path, path)
	}
}

// unknownCommandError refuses a command name cmd has no subcommand for,
// offering cobra's suggestions when it has any and the command list when it
// has none.
func unknownCommandError(cmd *cobra.Command, name string, suggestions []string) error {
	path := cmd.CommandPath()
	if len(suggestions) == 0 {
		return fmt.Errorf("'%s' isn't a %s command — see '%s --help' for the list.",
			name, path, path)
	}
	quoted := make([]string, len(suggestions))
	for i, suggestion := range suggestions {
		quoted[i] = "'" + suggestion + "'"
	}
	return fmt.Errorf("'%s' isn't a %s command — did you mean %s?",
		name, path, joinOr(quoted))
}

// cobraUnknownCommand matches the one refusal cobra raises from inside
// Execute, before any hook of kx's runs: an unknown command under the root.
// Cobra exports no type for it, so the message is all there is to go on;
// TestUnknownCommandSpeaksInKxsVoice fails if cobra ever rewords it.
var cobraUnknownCommand = regexp.MustCompile(`^unknown command "(.*)" for "(.*?)"`)

// rewriteUnknownCommand puts cobra's unknown-command refusal into kx's words.
// The suggestions are asked for again rather than parsed out of cobra's
// "Did you mean this?" block, which is layout rather than data.
func rewriteUnknownCommand(root *cobra.Command, err error) error {
	if err == nil {
		return nil
	}
	match := cobraUnknownCommand.FindStringSubmatch(err.Error())
	if match == nil {
		return err
	}
	cmd := root
	if path := strings.Fields(match[2]); len(path) > 1 {
		if found, _, findErr := root.Find(path[1:]); findErr == nil {
			cmd = found
		}
	}
	return unknownCommandError(cmd, match[1], cmd.SuggestionsFor(match[1]))
}

// ownFlagError is the root's flag-error function, inherited by every command
// that doesn't install its own, and the fallback of the one forwarding
// commands do (flagError). It covers pflag's four typed refusals; anything
// else passes through as pflag wrote it.
func ownFlagError(cmd *cobra.Command, err error) error {
	path := cmd.CommandPath()
	var invalid *pflag.InvalidValueError
	if errors.As(err, &invalid) {
		return fmt.Errorf("Invalid value for '--%s': '%s' is not a valid %s.",
			invalid.GetFlag().Name, invalid.GetValue(), invalid.GetFlag().Value.Type())
	}
	var missing *pflag.NotExistError
	if errors.As(err, &missing) {
		return fmt.Errorf("'%s' isn't a flag of %s — see '%s --help' for usage.",
			typedSpelling(missing.GetSpecifiedName(), missing.GetSpecifiedShortnames()), path, path)
	}
	var required *pflag.ValueRequiredError
	if errors.As(err, &required) {
		return fmt.Errorf("'%s' needs a value.",
			typedSpelling(required.GetSpecifiedName(), required.GetSpecifiedShortnames()))
	}
	var syntax *pflag.InvalidSyntaxError
	if errors.As(err, &syntax) {
		return fmt.Errorf("'%s' isn't a flag — a flag starts with one dash or two.",
			syntax.GetSpecifiedFlag())
	}
	return err
}

// typedSpelling writes a flag back the way it was typed: -n for a shorthand,
// --namespace for a long name. pflag reports a shorthand by its letter and the
// group it came in (the "n" of -An), and the letter is the part that failed.
func typedSpelling(name, shorthands string) string {
	if shorthands != "" {
		return "-" + name
	}
	return "--" + name
}
