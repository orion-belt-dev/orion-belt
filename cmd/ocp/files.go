package main

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"github.com/zrougamed/orion-belt/pkg/cliflags"
	"github.com/zrougamed/orion-belt/pkg/sdk"
)

// remoteTarget is a machine[:path] argument, the same shape ocp's copy
// arguments use.
type remoteTarget struct {
	machine string
	path    string
}

func parseRemoteTarget(arg string) (remoteTarget, error) {
	machine, path, ok := strings.Cut(arg, ":")
	if !ok || machine == "" {
		return remoteTarget{}, fmt.Errorf("invalid target %q (want machine:/path)", arg)
	}
	if path == "" {
		path = "."
	}
	return remoteTarget{machine: machine, path: path}, nil
}

func newLsCmd() *cobra.Command {
	var remoteUser string
	ls := &cobra.Command{
		Use:   "ls machine:/path",
		Short: "List a directory on a target machine",
		Long: `Lists a remote directory through the gateway, without opening a shell.

This is the listing the console's file browser shows. Copies still go over the
SSH tunnel: "ocp machine:/path ./local".`,
		Args: cobra.ExactArgs(1),
		Run: func(cmd *cobra.Command, args []string) {
			target, err := parseRemoteTarget(args[0])
			if err != nil {
				cliflags.Fatalf("%v", err)
			}
			runLs(target, remoteUser)
		},
	}
	ls.Flags().StringVar(&remoteUser, "remote-user", "", "UNIX user on the target host")
	return ls
}

func runLs(target remoteTarget, remoteUser string) {
	listing, err := fileAPI().ListFiles(context.Background(), target.machine, target.path, remoteUser)
	if err != nil {
		cliflags.Fatalf("listing %s: %v", target.path, err)
	}

	if flags.JSON {
		cliflags.MustPrintJSON(listing)
		return
	}

	// Some targets only return raw `ls` output (no structured entries), for
	// example when the remote shell is restricted.
	if len(listing.Entries) == 0 {
		if listing.Raw != "" {
			cliflags.Print("%s", strings.TrimRight(listing.Raw, "\n"))
			return
		}
		if listing.Path == "" {
			// The gateway builds listings by running a helper on the target;
			// a host missing its interpreter answers with nothing at all,
			// which is not the same as an empty directory.
			cliflags.Print("%s returned no listing for %s — the host may not support remote directory listing.",
				target.machine, target.path)
			return
		}
		cliflags.Print("(empty)")
		return
	}

	table := cliflags.NewTable("TYPE", "SIZE", "MODIFIED", "NAME")
	for _, entry := range listing.Entries {
		kind := "file"
		if entry.IsDir {
			kind = "dir"
		}
		modified := "-"
		if entry.MTime > 0 {
			modified = time.Unix(entry.MTime, 0).Format(time.RFC3339)
		}
		table.Row(kind, entry.Size, modified, entry.Name)
	}
	table.Flush()
}

func newMkdirCmd() *cobra.Command {
	var remoteUser string
	mkdir := &cobra.Command{
		Use:   "mkdir machine:/path",
		Short: "Create a directory on a target machine",
		Args:  cobra.ExactArgs(1),
		Run: func(cmd *cobra.Command, args []string) {
			target, err := parseRemoteTarget(args[0])
			if err != nil {
				cliflags.Fatalf("%v", err)
			}
			if err := fileAPI().MakeDir(context.Background(), target.machine, target.path, remoteUser); err != nil {
				cliflags.Fatalf("creating %s: %v", target.path, err)
			}
			cliflags.Print("✓ Created %s on %s", target.path, target.machine)
		},
	}
	mkdir.Flags().StringVar(&remoteUser, "remote-user", "", "UNIX user on the target host")
	return mkdir
}

func newRmCmd() *cobra.Command {
	var (
		remoteUser string
		force      bool
	)
	rm := &cobra.Command{
		Use:   "rm machine:/path",
		Short: "Delete a path on a target machine",
		Long:  `Deletes a remote file or directory. The deletion is recorded in the audit log against your identity.`,
		Args:  cobra.ExactArgs(1),
		Run: func(cmd *cobra.Command, args []string) {
			target, err := parseRemoteTarget(args[0])
			if err != nil {
				cliflags.Fatalf("%v", err)
			}
			if !force && !confirm(fmt.Sprintf("Delete %s on %s?", target.path, target.machine)) {
				cliflags.Print("Aborted.")
				return
			}
			if err := fileAPI().DeleteFile(context.Background(), target.machine, target.path, remoteUser); err != nil {
				cliflags.Fatalf("deleting %s: %v", target.path, err)
			}
			cliflags.Print("✓ Deleted %s on %s", target.path, target.machine)
		},
	}
	rm.Flags().StringVar(&remoteUser, "remote-user", "", "UNIX user on the target host")
	rm.Flags().BoolVarP(&force, "force", "f", false, "delete without confirming")
	return rm
}

// confirm asks for an interactive yes before a destructive action.
func confirm(question string) bool {
	answer, err := cliflags.PromptLine(question + " [y/N]: ")
	if err != nil {
		return false
	}
	answer = strings.ToLower(strings.TrimSpace(answer))
	return answer == "y" || answer == "yes"
}

// fileAPI returns an authenticated SDK client for the remote file endpoints.
func fileAPI() *sdk.Client {
	client, err := flags.APIClient()
	if err != nil {
		cliflags.Fatalf("%v", err)
	}
	return client
}
