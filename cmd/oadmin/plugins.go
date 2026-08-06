package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/spf13/cobra"
	"github.com/zrougamed/orion-belt/pkg/cliflags"
	"github.com/zrougamed/orion-belt/pkg/sdk"
)

func newPluginsCmd() *cobra.Command {
	plugins := &cobra.Command{
		Use:   "plugins",
		Short: "Manage plugins",
		Long:  `Inspect plugin status and configuration, and enable or disable plugins.`,
	}

	var showSchema bool
	list := &cobra.Command{
		Use:   "list",
		Short: "List plugins and their status",
		Args:  cobra.NoArgs,
		Run: func(cmd *cobra.Command, args []string) {
			runPluginsList(showSchema)
		},
	}
	list.Flags().BoolVar(&showSchema, "schema", false, "also print each plugin's configuration fields")
	plugins.AddCommand(list)

	plugins.AddCommand(&cobra.Command{
		Use:   "enable [name]",
		Short: "Enable a plugin",
		Args:  cobra.ExactArgs(1),
		Run: func(cmd *cobra.Command, args []string) {
			plugin, err := api().EnablePlugin(ctx(), args[0])
			if err != nil {
				cliflags.Fatalf("enabling plugin: %v", err)
			}
			reportPlugin(plugin, "enabled")
		},
	})

	plugins.AddCommand(&cobra.Command{
		Use:   "disable [name]",
		Short: "Disable a plugin",
		Args:  cobra.ExactArgs(1),
		Run: func(cmd *cobra.Command, args []string) {
			plugin, err := api().DisablePlugin(ctx(), args[0])
			if err != nil {
				cliflags.Fatalf("disabling plugin: %v", err)
			}
			reportPlugin(plugin, "disabled")
		},
	})

	var (
		settings []string
		fromFile string
		enable   bool
		disable  bool
	)
	config := &cobra.Command{
		Use:   "config [name]",
		Short: "Set a plugin's configuration",
		Long: `Replaces a plugin's configuration.

Values come from repeated --set key=value pairs or from a JSON document with
--from-file (use "-" for stdin). The submitted config replaces the stored one,
so include every field the plugin needs, and note that secrets given on the
command line land in your shell history.`,
		Args: cobra.ExactArgs(1),
		Run: func(cmd *cobra.Command, args []string) {
			if enable && disable {
				cliflags.Fatalf("--enable and --disable are mutually exclusive")
			}
			config, err := pluginConfig(settings, fromFile)
			if err != nil {
				cliflags.Fatalf("%v", err)
			}

			client := api()
			enabled := enable
			if !enable && !disable {
				// Neither flag given: keep the plugin's current state rather
				// than silently disabling it, since the API takes an absolute
				// enabled value.
				current, cerr := findPlugin(client, args[0])
				if cerr != nil {
					cliflags.Fatalf("%v", cerr)
				}
				enabled = current.Enabled
			}

			plugin, configureErr, err := client.UpdatePluginConfig(ctx(), args[0], enabled, config)
			if err != nil {
				cliflags.Fatalf("configuring plugin: %v", err)
			}
			if flags.JSON {
				cliflags.MustPrintJSON(plugin)
				return
			}
			cliflags.Print("✓ Plugin %s configured (enabled=%s)", plugin.Name, cliflags.YesNo(plugin.Enabled))
			if configureErr != "" {
				cliflags.Print("! The plugin reported a configuration error: %s", configureErr)
			}
		},
	}
	config.Flags().StringArrayVar(&settings, "set", nil, "config value as key=value (repeatable)")
	config.Flags().StringVar(&fromFile, "from-file", "", "read the config as JSON from this file (\"-\" for stdin)")
	config.Flags().BoolVar(&enable, "enable", false, "enable the plugin as part of this update")
	config.Flags().BoolVar(&disable, "disable", false, "disable the plugin as part of this update")
	plugins.AddCommand(config)

	return plugins
}

func runPluginsList(showSchema bool) {
	plugins, err := api().ListPlugins(ctx())
	if err != nil {
		cliflags.Fatalf("listing plugins: %v", err)
	}

	if flags.JSON {
		cliflags.MustPrintJSON(plugins)
		return
	}
	if len(plugins) == 0 {
		cliflags.Print("No plugins registered.")
		return
	}

	table := cliflags.NewTable("NAME", "VERSION", "ENABLED", "CONFIGURED", "WEBHOOK", "LAST ERROR")
	for _, plugin := range plugins {
		table.Row(plugin.Name, cliflags.OrDash(plugin.Version), cliflags.YesNo(plugin.Enabled),
			cliflags.YesNo(plugin.Configured), cliflags.YesNo(plugin.HasWebhook),
			cliflags.Truncate(cliflags.OrDash(plugin.LastError), 40))
	}
	table.Flush()

	if !showSchema {
		return
	}
	for _, plugin := range plugins {
		if len(plugin.Schema) == 0 {
			continue
		}
		cliflags.Print("\n%s configuration fields:", plugin.Name)
		for _, field := range plugin.Schema {
			required := ""
			if field.Required {
				required = " (required)"
			}
			cliflags.Print("  %-24s %s%s", field.Key, field.Type, required)
		}
	}
}

func reportPlugin(plugin *sdk.PluginInfo, verb string) {
	if flags.JSON {
		cliflags.MustPrintJSON(plugin)
		return
	}
	cliflags.Print("✓ Plugin %s %s", plugin.Name, verb)
	if plugin.LastError != "" {
		cliflags.Print("! Last error: %s", plugin.LastError)
	}
}

func findPlugin(client *sdk.Client, name string) (*sdk.PluginInfo, error) {
	plugins, err := client.ListPlugins(ctx())
	if err != nil {
		return nil, err
	}
	for i := range plugins {
		if plugins[i].Name == name {
			return &plugins[i], nil
		}
	}
	return nil, fmt.Errorf("no plugin named %q", name)
}

// pluginConfig builds the config document from --set pairs, a JSON file, or
// both, with --set winning so a single value can be overridden on top of a
// stored document.
func pluginConfig(settings []string, fromFile string) (map[string]interface{}, error) {
	config := map[string]interface{}{}

	if fromFile != "" {
		var (
			data []byte
			err  error
		)
		if fromFile == "-" {
			data, err = io.ReadAll(os.Stdin)
		} else {
			data, err = os.ReadFile(fromFile)
		}
		if err != nil {
			return nil, fmt.Errorf("read plugin config: %w", err)
		}
		if err := json.Unmarshal(data, &config); err != nil {
			return nil, fmt.Errorf("parse plugin config JSON: %w", err)
		}
	}

	for _, setting := range settings {
		key, value, ok := strings.Cut(setting, "=")
		if !ok || strings.TrimSpace(key) == "" {
			return nil, fmt.Errorf("invalid --set value %q (want key=value)", setting)
		}
		config[strings.TrimSpace(key)] = value
	}

	if len(config) == 0 {
		return nil, fmt.Errorf("no configuration given: pass --set key=value or --from-file")
	}
	return config, nil
}
