package main

import (
	"fmt"
	"sort"
	"strings"

	"github.com/spf13/cobra"
	"github.com/zrougamed/orion-belt/pkg/cliflags"
	"github.com/zrougamed/orion-belt/pkg/sdk"
)

func newNotificationsCmd() *cobra.Command {
	notifications := &cobra.Command{
		Use:   "notifications",
		Short: "Manage notification policy",
		Long: `Inspect and set the organization-wide notification policy.

The policy bounds what users may choose for themselves: which channels they can
enable, and which events are delivered whatever their preferences say. Users
manage their own preferences with "osh notifications prefs".`,
	}

	notifications.AddCommand(&cobra.Command{
		Use:   "policy",
		Short: "Show the notification policy",
		Args:  cobra.NoArgs,
		Run:   runNotificationPolicy,
	})

	var (
		channels  []string
		mandatory []string
		clear     bool
	)
	setPolicy := &cobra.Command{
		Use:   "set-policy",
		Short: "Replace the notification policy",
		Long: `Replaces the notification policy.

  --allow-channel in_app --allow-channel email
      restrict users to these channels (omit entirely to allow every channel)

  --mandatory access_request.approved=email
      always deliver that event on that channel, whatever the user prefers
      (repeatable; repeat the event to name several channels)

  --clear
      restore the permissive default: every channel selectable, nothing forced

The policy is replaced wholesale, so pass the complete set of rules you want.`,
		Args: cobra.NoArgs,
		Run: func(cmd *cobra.Command, args []string) {
			runSetNotificationPolicy(channels, mandatory, clear)
		},
	}
	setPolicy.Flags().StringArrayVar(&channels, "allow-channel", nil, "channel users may enable (repeatable; empty allows all)")
	setPolicy.Flags().StringArrayVar(&mandatory, "mandatory", nil, "always-delivered event as event=channel (repeatable)")
	setPolicy.Flags().BoolVar(&clear, "clear", false, "reset to the permissive default")
	notifications.AddCommand(setPolicy)

	return notifications
}

func runNotificationPolicy(cmd *cobra.Command, args []string) {
	result, err := api().GetNotificationPolicy(ctx())
	if err != nil {
		cliflags.Fatalf("getting notification policy: %v", err)
	}

	if flags.JSON {
		cliflags.MustPrintJSON(result)
		return
	}

	allowed := "all channels"
	if len(result.Policy.AllowedChannels) > 0 {
		allowed = strings.Join(result.Policy.AllowedChannels, ", ")
	}
	cliflags.Print("Selectable channels: %s", allowed)
	cliflags.Print("Known channels:      %s", strings.Join(result.KnownChannels, ", "))

	if len(result.Policy.MandatoryEvents) == 0 {
		cliflags.Print("Mandatory events:    none")
	} else {
		cliflags.Print("Mandatory events:")
		events := make([]string, 0, len(result.Policy.MandatoryEvents))
		for event := range result.Policy.MandatoryEvents {
			events = append(events, event)
		}
		sort.Strings(events)
		for _, event := range events {
			cliflags.Print("  %-40s %s", event, strings.Join(result.Policy.MandatoryEvents[event], ", "))
		}
	}

	if !result.Policy.UpdatedAt.IsZero() {
		cliflags.Print("Updated:             %s", cliflags.FormatTime(result.Policy.UpdatedAt))
	}
}

func runSetNotificationPolicy(channels, mandatory []string, clear bool) {
	if clear && (len(channels) > 0 || len(mandatory) > 0) {
		cliflags.Fatalf("--clear cannot be combined with --allow-channel or --mandatory")
	}
	if !clear && len(channels) == 0 && len(mandatory) == 0 {
		cliflags.Fatalf("nothing to set: pass --allow-channel, --mandatory, or --clear")
	}

	policy := sdk.NotificationPolicy{MandatoryEvents: map[string][]string{}}
	if !clear {
		policy.AllowedChannels = channels
		events, err := parseMandatoryEvents(mandatory)
		if err != nil {
			cliflags.Fatalf("%v", err)
		}
		policy.MandatoryEvents = events
	}

	updated, err := api().PutNotificationPolicy(ctx(), policy)
	if err != nil {
		cliflags.Fatalf("setting notification policy: %v", err)
	}

	if flags.JSON {
		cliflags.MustPrintJSON(updated)
		return
	}
	cliflags.Print("✓ Notification policy updated")
	if len(updated.AllowedChannels) == 0 {
		cliflags.Print("  Selectable channels: all")
	} else {
		cliflags.Print("  Selectable channels: %s", strings.Join(updated.AllowedChannels, ", "))
	}
	cliflags.Print("  Mandatory events:    %d", len(updated.MandatoryEvents))
}

// parseMandatoryEvents turns repeated event=channel pairs into the map shape
// the API expects, merging channels named across several flags for one event.
func parseMandatoryEvents(values []string) (map[string][]string, error) {
	events := map[string][]string{}
	for _, value := range values {
		event, channel, ok := strings.Cut(value, "=")
		event, channel = strings.TrimSpace(event), strings.TrimSpace(channel)
		if !ok || event == "" || channel == "" {
			return nil, fmt.Errorf("invalid --mandatory value %q (want event=channel)", value)
		}
		if !contains(events[event], channel) {
			events[event] = append(events[event], channel)
		}
	}
	return events, nil
}

func contains(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}
