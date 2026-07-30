package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/orion-belt-dev/orion-belt/pkg/agent"
	"github.com/orion-belt-dev/orion-belt/pkg/common"
	"github.com/orion-belt-dev/orion-belt/pkg/tracing"
	"github.com/orion-belt-dev/orion-belt/pkg/version"
	"github.com/spf13/cobra"
)

// tracingFlushTimeout bounds how long shutdown waits for buffered spans to
// reach the collector, so an unreachable collector cannot hang an agent
// restart.
const tracingFlushTimeout = 5 * time.Second

// agentServiceName derives the trace service name from the agent's configured
// name. Every agent reporting as a single "orion-belt-agent" service would
// make traces from a fleet indistinguishable, which defeats the point of
// tracing the agent hop at all. An explicit tracing.service_name still wins.
func agentServiceName(config *common.Config) string {
	if name := strings.TrimSpace(config.Agent.Name); name != "" {
		return "orion-belt-agent-" + name
	}
	return "orion-belt-agent"
}

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
	Use:     "orion-belt-agent",
	Short:   "Orion-Belt Agent",
	Long:    `Orion-Belt agent runs on target machines to receive SSH connections.`,
	Version: version.String(),
	Run:     runAgent,
}

func init() {
	rootCmd.PersistentFlags().StringVarP(&configFile, "config", "c", "/etc/orion-belt/agent.yaml", "config file path")
	rootCmd.PersistentFlags().StringVarP(&logLevel, "log-level", "l", "info", "log level (debug, info, warn, error)")
}

func main() {
	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}

func runAgent(cmd *cobra.Command, args []string) {
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
	config, err := common.LoadConfig(configFile)
	if err != nil {
		logger.Fatal("Failed to load config: %v", err)
	}

	// Start tracing before the agent connects, so the first session the
	// gateway opens can already be linked. A no-op when tracing.enabled is
	// false. The agent's service name defaults to include its configured
	// name, since a trace is only useful if you can tell which agent it hit.
	shutdownTracing, err := tracing.Init(context.Background(),
		tracing.FromCommon(config.Tracing, agentServiceName(config), version.Version), logger)
	if err != nil {
		logger.Fatal("Failed to initialize tracing: %v", err)
	}
	defer flushTracing(shutdownTracing, logger)

	// Create agent
	agt, err := agent.New(config, logger)
	if err != nil {
		logger.Fatal("Failed to create agent: %v", err)
	}

	// Set up signal handling
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)

	// Start agent in goroutine
	errChan := make(chan error, 1)
	go func() {
		errChan <- agt.Start()
	}()

	// Wait for shutdown signal or error
	select {
	case sig := <-sigChan:
		logger.Info("Received signal: %v", sig)
		ctx := context.Background()
		if err := agt.Stop(ctx); err != nil {
			logger.Error("Error stopping agent: %v", err)
		}
	case err := <-errChan:
		if err != nil {
			logger.Fatal("Agent error: %v", err)
		}
	}

	logger.Info("Agent stopped")
}
