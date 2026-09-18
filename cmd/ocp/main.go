package main

import (
	"fmt"
	"os"
	"strings"

	"github.com/orion-belt-dev/orion-belt/pkg/client"
	"github.com/orion-belt-dev/orion-belt/pkg/cliflags"
	"github.com/orion-belt-dev/orion-belt/pkg/version"
	"github.com/spf13/cobra"
)

var (
	flags     cliflags.Common
	recursive bool
)

var rootCmd = &cobra.Command{
	Use:   "ocp source destination",
	Short: "Orion-Belt SCP Client",
	Long: `ocp copies files through the Orion-Belt gateway (SCP over the reverse tunnel).

Its subcommands browse the same remote filesystems the console file browser
shows: "ocp ls", "ocp mkdir", and "ocp rm".`,
	Version: version.String(),
	Args:    cobra.ExactArgs(2),
	Run:     runSCP,
}

func init() {
	flags.BindPersistent(rootCmd)
	flags.BindSSHTrust(rootCmd)
	rootCmd.Flags().BoolVarP(&recursive, "recursive", "r", false, "recursively copy directories (not yet supported)")
	_ = rootCmd.Flags().MarkHidden("recursive") // registered for compatibility; Copy() does not implement it yet

	rootCmd.AddCommand(newLsCmd())
	rootCmd.AddCommand(newMkdirCmd())
	rootCmd.AddCommand(newRmCmd())
}

func main() {
	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}

func runSCP(cmd *cobra.Command, args []string) {
	logger := flags.Logger()
	config, err := flags.LoadConfig()
	if err != nil {
		logger.Fatal("%v", err)
	}

	if recursive {
		logger.Fatal("--recursive is not supported yet")
	}

	scpClient, err := client.NewSCPClient(config, logger)
	if err != nil {
		logger.Fatal("Failed to create SCP client: %v", err)
	}

	source := args[0]
	destination := args[1]
	isUpload := !strings.Contains(source, ":")

	user, err := flags.Username(config)
	if err != nil {
		logger.Fatal("%v", err)
	}

	if err := scpClient.Copy(user, source, destination, isUpload); err != nil {
		logger.Fatal("Copy failed: %v", err)
	}
}
