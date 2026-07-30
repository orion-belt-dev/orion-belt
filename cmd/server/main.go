package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/orion-belt-dev/orion-belt/pkg/common"
	"github.com/orion-belt-dev/orion-belt/pkg/server"
	"github.com/orion-belt-dev/orion-belt/pkg/tracing"
	"github.com/orion-belt-dev/orion-belt/pkg/version"
	"github.com/spf13/cobra"
)

// tracingFlushTimeout bounds how long shutdown waits for buffered spans to
// reach the collector. Losing the last few spans is preferable to hanging a
// restart on an unreachable collector.
const tracingFlushTimeout = 5 * time.Second

// flushTracing drains buffered spans on shutdown. Safe to defer
// unconditionally: when tracing is disabled the shutdown func is a no-op.
func flushTracing(shutdown tracing.ShutdownFunc, logger *common.Logger) {
	if shutdown == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), tracingFlushTimeout)
	defer cancel()
	if err := shutdown(ctx); err != nil {
		logger.Warn("Error flushing traces: %v", err)
	}
}

var (
	configFile string
	logLevel   string
)

var rootCmd = &cobra.Command{
	Use:     "orion-belt-server",
	Short:   "Orion-Belt SSH Tunneling Server",
	Long:    `Orion-Belt is a secure SSH/SCP tunneling and session recording system with ReBAC and temporary access management.`,
	Version: version.String(),
}

var startCmd = &cobra.Command{
	Use:   "start",
	Short: "Start the Orion-Belt server",
	Long:  `Start the Orion-Belt SSH/SCP tunneling server with session recording and access control.`,
	Run:   runServer,
}

func init() {
	rootCmd.PersistentFlags().StringVarP(&configFile, "config", "c", "", "config file path")
	rootCmd.PersistentFlags().StringVarP(&logLevel, "log-level", "l", "info", "log level (debug, info, warn, error)")
	rootCmd.AddCommand(startCmd)
	rootCmd.AddCommand(agentCmd)
	rootCmd.AddCommand(userCmd)
	rootCmd.AddCommand(permissionCmd)
	rootCmd.AddCommand(sessionCmd)
	rootCmd.Run = runServer
}

func main() {
	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}

func runServer(cmd *cobra.Command, args []string) {
	// Parse log level
	level := common.INFO
	switch logLevel {
	case "debug":
		level = common.DEBUG
	case "warn":
		level = common.WARN
	case "error":
		level = common.ERROR
	}

	logger := common.NewLogger(level)

	// Load configuration
	config, err := loadConfig()
	if err != nil {
		logger.Fatal("Failed to load config: %v", err)
	}

	// Start tracing before the server so gateway spans exist from the first
	// session. A no-op when tracing.enabled is false, so this is always safe
	// to call and always safe to defer.
	shutdownTracing, err := tracing.Init(context.Background(),
		tracing.FromCommon(config.Tracing, "orion-belt-gateway", version.Version), logger)
	if err != nil {
		// Config validation failed (missing service name, bad ratio, etc.).
		// An unreachable collector does not fail Init — OTLP New is
		// non-blocking — and surfaces later as exporter warnings.
		logger.Fatal("Failed to initialize tracing: %v", err)
	}
	defer flushTracing(shutdownTracing, logger)

	// Create server
	srv, err := server.New(config, logger)
	if err != nil {
		logger.Fatal("Failed to create server: %v", err)
	}

	// Set up signal handling
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)

	// Start server in goroutine
	errChan := make(chan error, 1)
	go func() {
		errChan <- srv.Start()
	}()

	// Wait for shutdown signal or error
	select {
	case sig := <-sigChan:
		logger.Info("Received signal: %v", sig)
		ctx := context.Background()
		if err := srv.Stop(ctx); err != nil {
			logger.Error("Error stopping server: %v", err)
		}
	case err := <-errChan:
		if err != nil {
			logger.Fatal("Server error: %v", err)
		}
	}

	logger.Info("Server stopped")
}

func getLogger() *common.Logger {
	level := common.INFO
	switch logLevel {
	case "debug":
		level = common.DEBUG
	case "warn":
		level = common.WARN
	case "error":
		level = common.ERROR
	}
	return common.NewLogger(level)
}

// loadConfig attempts to load configuration from multiple paths
func loadConfig() (*common.Config, error) {
	if configFile != "" {
		return common.LoadConfig(configFile)
	}
	execPath, err := os.Executable()
	if err != nil {
		execPath = "."
	}
	execDir := filepath.Dir(execPath)

	configPaths := []string{
		"/etc/orion-belt/server.yaml",
		filepath.Join(execDir, "../config/server.yaml"),
		filepath.Join(execDir, "config/server.yaml"),
		"./config/server.yaml",
		"./server.yaml",
	}

	var lastErr error
	for _, path := range configPaths {
		if _, err := os.Stat(path); err == nil {
			config, err := common.LoadConfig(path)
			if err == nil {
				return config, nil
			}
			lastErr = err
		}
	}

	if lastErr != nil {
		return nil, fmt.Errorf("config file found but failed to load: %w", lastErr)
	}

	return nil, fmt.Errorf("no config file found in any of the following locations:\n  - %s",
		formatPaths(configPaths))
}

// formatPaths formats a list of paths for error messages
func formatPaths(paths []string) string {
	result := ""
	for i, path := range paths {
		if i > 0 {
			result += "\n  - "
		}
		result += path
	}
	return result
}
