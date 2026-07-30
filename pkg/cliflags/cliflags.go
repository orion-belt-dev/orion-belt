// Package cliflags registers shared persistent flags for osh, ocp, and oadmin
// and applies them on top of the YAML client config.
package cliflags

import (
	"fmt"
	"os"
	"time"

	"github.com/orion-belt-dev/orion-belt/pkg/common"
	"github.com/spf13/cobra"
)

// Common is the shared flag set for client CLIs.
type Common struct {
	ConfigFile  string
	User        string
	APIEndpoint string
	Identity    string
	Insecure    bool // maps to auth.strict_host_key_checking=no
	Verbose     bool
	JSON        bool
	Timeout     time.Duration

	// SSH gateway overrides (osh / ocp)
	ProxyHost string
	ProxyPort int
}

// BindPersistent registers flags that every subcommand should inherit.
func (c *Common) BindPersistent(cmd *cobra.Command) {
	cmd.PersistentFlags().StringVarP(&c.ConfigFile, "config", "c", defaultConfigPath(), "client config file")
	cmd.PersistentFlags().StringVarP(&c.User, "user", "u", "", "gateway username (overrides auth.user / $USER / $ORION_USER)")
	cmd.PersistentFlags().StringVar(&c.APIEndpoint, "api-endpoint", "", "HTTP API base URL (overrides server.api_endpoint / $ORION_API_ENDPOINT)")
	cmd.PersistentFlags().StringVarP(&c.Identity, "identity", "i", "", "SSH private key path (overrides auth.key_file)")
	cmd.PersistentFlags().BoolVarP(&c.Verbose, "verbose", "v", false, "verbose logging")
	cmd.PersistentFlags().BoolVar(&c.JSON, "json", false, "machine-readable JSON where supported")
	cmd.PersistentFlags().DurationVar(&c.Timeout, "timeout", 30*time.Second, "HTTP / dial timeout")
}

// BindSSHTrust adds lab-friendly host-key overrides (osh / ocp).
func (c *Common) BindSSHTrust(cmd *cobra.Command) {
	cmd.PersistentFlags().BoolVar(&c.Insecure, "insecure", false, "skip host-key verification (sets strict_host_key_checking=no)")
	cmd.PersistentFlags().Bool("no-host-key-check", false, "alias for --insecure")
	cmd.PersistentFlags().StringVar(&c.ProxyHost, "proxy", "", "gateway SSH hostname (overrides server.host)")
	cmd.PersistentFlags().IntVar(&c.ProxyPort, "proxy-port", 0, "gateway SSH port (overrides server.port)")

	cmd.PersistentPreRunE = chainPreRun(cmd.PersistentPreRunE, func(cmd *cobra.Command, args []string) error {
		if v, err := cmd.Flags().GetBool("no-host-key-check"); err == nil && v {
			c.Insecure = true
		}
		return nil
	})
}

func chainPreRun(existing func(*cobra.Command, []string) error, next func(*cobra.Command, []string) error) func(*cobra.Command, []string) error {
	return func(cmd *cobra.Command, args []string) error {
		if existing != nil {
			if err := existing(cmd, args); err != nil {
				return err
			}
		}
		return next(cmd, args)
	}
}

func defaultConfigPath() string {
	if v := os.Getenv("ORION_CONFIG"); v != "" {
		return v
	}
	return os.ExpandEnv("$HOME/.orion-belt/client.yaml")
}

// Username resolves the gateway username after LoadConfig.
func (c *Common) Username(cfg *common.Config) (string, error) {
	if c.User != "" {
		return c.User, nil
	}
	if cfg.Auth.User != "" {
		return cfg.Auth.User, nil
	}
	if u := os.Getenv("ORION_USER"); u != "" {
		return u, nil
	}
	if u := os.Getenv("USER"); u != "" {
		return u, nil
	}
	return "", fmt.Errorf("username not set: use --user, auth.user in config, or $ORION_USER")
}

// Logger returns INFO or DEBUG based on --verbose.
func (c *Common) Logger() *common.Logger {
	if c.Verbose {
		return common.NewLogger(common.DEBUG)
	}
	return common.NewLogger(common.INFO)
}
