package main

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"
)

var configCmd = &cobra.Command{
	Use:   "config",
	Short: "Configuration management",
	Long:  `Manage and inspect Orion-Belt server configuration.`,
}

var configShowCmd = &cobra.Command{
	Use:   "show",
	Short: "Show configuration",
	Long:  `Display the current configuration and its source.`,
	Run:   runConfigShow,
}

var configPathCmd = &cobra.Command{
	Use:   "path",
	Short: "Show config file path",
	Long:  `Display the path to the configuration file that will be used.`,
	Run:   runConfigPath,
}

var configLocationsCmd = &cobra.Command{
	Use:   "locations",
	Short: "Show all config search paths",
	Long:  `Display all paths where the server will search for configuration files.`,
	Run:   runConfigLocations,
}

func init() {
	configCmd.AddCommand(configShowCmd)
	configCmd.AddCommand(configPathCmd)
	configCmd.AddCommand(configLocationsCmd)
	rootCmd.AddCommand(configCmd)
}

func runConfigShow(cmd *cobra.Command, args []string) {
	config, err := loadConfig()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error loading config: %v\n", err)
		os.Exit(1)
	}

	fmt.Println("Configuration loaded successfully:")
	fmt.Printf("\nServer:\n")
	fmt.Printf("  Host:         %s\n", config.Server.Host)
	fmt.Printf("  Port:         %d\n", config.Server.Port)
	fmt.Printf("  API Port:     %d\n", config.Server.APIPort)
	fmt.Printf("  Public URL:   %s\n", config.Server.AdvertisedURL())
	fmt.Printf("  Public SSH:   %s:%d\n", config.Server.AdvertisedSSHHost(), config.Server.AdvertisedSSHPort())
	fmt.Printf("  UI:           %s\n", config.Server.UIURL())
	fmt.Printf("  SSH Host Key: %s\n", config.Server.SSHHostKey)

	fmt.Printf("\nDatabase:\n")
	fmt.Printf("  Driver:      %s\n", config.Database.Driver)
	fmt.Printf("  Connection:  %s\n", maskConnectionString(config.Database.ConnectionString))

	fmt.Printf("\nRecording:\n")
	fmt.Printf("  Enabled:     %v\n", config.Recording.Enabled)
	fmt.Printf("  Storage:     %s\n", config.Recording.StoragePath)
	fmt.Printf("  Retention:   %d days\n", config.Recording.RetentionDays)

	if len(config.Plugins) > 0 {
		fmt.Printf("\nPlugins:\n")
		for name := range config.Plugins {
			fmt.Printf("  - %s\n", name)
		}
	}
}

func runConfigPath(cmd *cobra.Command, args []string) {
	path, err := findConfigPath()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("Config file: %s\n", path)
}

func runConfigLocations(cmd *cobra.Command, args []string) {
	paths := getConfigPaths()

	fmt.Println("Config file search paths (in order):")
	for i, path := range paths {
		exists := "✗"
		if _, err := os.Stat(path); err == nil {
			exists = "✓"
		}
		fmt.Printf("  %d. [%s] %s\n", i+1, exists, path)
	}

	fmt.Println("\nNote: The first existing file will be used.")
	if configFile != "" {
		fmt.Printf("Explicitly specified: %s\n", configFile)
	}
}

// findConfigPath returns the path to the config file that will be used
func findConfigPath() (string, error) {
	if configFile != "" {
		if _, err := os.Stat(configFile); err == nil {
			return configFile, nil
		}
		return "", fmt.Errorf("specified config file not found: %s", configFile)
	}

	paths := getConfigPaths()
	for _, path := range paths {
		if _, err := os.Stat(path); err == nil {
			return path, nil
		}
	}

	return "", fmt.Errorf("no config file found in any search path")
}

// getConfigPaths returns all possible config file paths
func getConfigPaths() []string {
	execPath, err := os.Executable()
	if err != nil {
		execPath = "."
	}
	execDir := filepath.Dir(execPath)

	return []string{
		"/etc/orion-belt/server.yaml",
		filepath.Join(execDir, "../config/server.yaml"),
		filepath.Join(execDir, "config/server.yaml"),
		"./config/server.yaml",
		"./server.yaml",
	}
}

// maskConnectionString masks sensitive parts of connection string
func maskConnectionString(connStr string) string {
	// Simple masking - hide password if present
	// Format: postgres://user:password@host/db
	if len(connStr) == 0 {
		return ""
	}

	// Find password section
	for i := 0; i < len(connStr); i++ {
		if connStr[i] == ':' && i+1 < len(connStr) {
			// Found potential password start
			for j := i + 1; j < len(connStr); j++ {
				if connStr[j] == '@' {
					// Found password end
					masked := connStr[:i+1] + "****" + connStr[j:]
					return masked
				}
			}
		}
	}

	return connStr
}
