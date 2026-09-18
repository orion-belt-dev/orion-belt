package cliparity

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

// VerifyCommands checks that every invocation Coverage records for binary
// still resolves against its command tree: each subcommand exists, and each
// named flag is defined on (or inherited by) the command it is written under.
//
// This is the half of the parity check that catches a renamed or deleted
// command, where the route is still classified but the CLI no longer covers it.
func VerifyCommands(root *cobra.Command, binary string) []error {
	var errs []error
	for _, invocation := range CommandsFor(binary) {
		if err := verifyInvocation(root, binary, invocation); err != nil {
			errs = append(errs, err)
		}
	}
	return errs
}

func verifyInvocation(root *cobra.Command, binary, invocation string) error {
	tokens := strings.Fields(invocation)
	if len(tokens) == 0 || tokens[0] != binary {
		return fmt.Errorf("%q is not an invocation of %s", invocation, binary)
	}

	cmd := root
	for _, token := range tokens[1:] {
		if strings.HasPrefix(token, "--") {
			name := strings.TrimPrefix(token, "--")
			if cmd.Flags().Lookup(name) == nil && cmd.InheritedFlags().Lookup(name) == nil {
				return fmt.Errorf("%q: %q has no flag --%s", invocation, cmd.CommandPath(), name)
			}
			continue
		}
		child := findChild(cmd, token)
		if child == nil {
			return fmt.Errorf("%q: %q has no subcommand %q", invocation, cmd.CommandPath(), token)
		}
		cmd = child
	}

	if cmd != root && !cmd.Runnable() && !cmd.HasSubCommands() {
		return fmt.Errorf("%q resolves to %q, which does nothing", invocation, cmd.CommandPath())
	}
	return nil
}

func findChild(cmd *cobra.Command, name string) *cobra.Command {
	for _, child := range cmd.Commands() {
		if child.Name() == name || child.HasAlias(name) {
			return child
		}
	}
	return nil
}

// VerifyHelp checks that every command in a tree carries the help text users
// need: a short description, and a usage string on every flag.
func VerifyHelp(root *cobra.Command) []error {
	var errs []error
	walk(root, func(cmd *cobra.Command) {
		// Cobra generates `completion` and `help` itself.
		if cmd.Name() == "completion" || cmd.Name() == "help" {
			return
		}
		if strings.TrimSpace(cmd.Short) == "" {
			errs = append(errs, fmt.Errorf("%q has no Short description", cmd.CommandPath()))
		}
		if cmd.HasSubCommands() && !cmd.Runnable() && strings.TrimSpace(cmd.Long) == "" {
			errs = append(errs, fmt.Errorf("%q is a command group and needs a Long description explaining what it covers", cmd.CommandPath()))
		}
		cmd.LocalFlags().VisitAll(func(flag *pflag.Flag) {
			if strings.TrimSpace(flag.Usage) == "" {
				errs = append(errs, fmt.Errorf("%q: flag --%s has no usage text", cmd.CommandPath(), flag.Name))
			}
		})
	})
	return errs
}

func walk(cmd *cobra.Command, fn func(*cobra.Command)) {
	fn(cmd)
	for _, child := range cmd.Commands() {
		walk(child, fn)
	}
}
