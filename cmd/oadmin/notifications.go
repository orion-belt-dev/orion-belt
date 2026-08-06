package main

import (
	"fmt"
	"sort"
	"strings"

	"github.com/orion-belt-dev/orion-belt/pkg/cliflags"
	"github.com/orion-belt-dev/orion-belt/pkg/sdk"
	"github.com/spf13/cobra"
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

	var showBody bool
	templates := &cobra.Command{
		Use:   "templates",
		Short: "Show the notification copy for each event",
		Long: `Lists every templatable event with the wording recipients see.

Events left on the built-in copy are marked as defaults; only a customized
event has anything for "reset-template" to undo.`,
		Args: cobra.NoArgs,
		Run: func(cmd *cobra.Command, args []string) {
			runNotificationTemplates(showBody)
		},
	}
	templates.Flags().BoolVar(&showBody, "body", false, "also print each template's body and placeholders")
	notifications.AddCommand(templates)

	var (
		title string
		body  string
	)
	setTemplate := &cobra.Command{
		Use:   "set-template [event]",
		Short: "Override the copy for one event",
		Long: `Replaces the title and body recipients see for one notification event.

Both are required — the override replaces the built-in copy wholesale rather
than patching it. Run "oadmin notifications templates --body" first to see the
placeholders the event supports; the server rejects copy that uses others.`,
		Args: cobra.ExactArgs(1),
		Run: func(cmd *cobra.Command, args []string) {
			runSetNotificationTemplate(args[0], title, body)
		},
	}
	setTemplate.Flags().StringVar(&title, "title", "", "notification title (required)")
	setTemplate.Flags().StringVar(&body, "body", "", "notification body (required)")
	_ = setTemplate.MarkFlagRequired("title")
	_ = setTemplate.MarkFlagRequired("body")
	notifications.AddCommand(setTemplate)

	notifications.AddCommand(&cobra.Command{
		Use:   "reset-template [event]",
		Short: "Drop an event's override and return it to the built-in copy",
		Args:  cobra.ExactArgs(1),
		Run: func(cmd *cobra.Command, args []string) {
			if err := api().DeleteNotificationTemplate(ctx(), args[0]); err != nil {
				cliflags.Fatalf("resetting template: %v", err)
			}
			cliflags.Print("✓ %s restored to the built-in copy", args[0])
		},
	})

	return notifications
}

func runNotificationTemplates(showBody bool) {
	templates, err := api().ListNotificationTemplates(ctx())
	if err != nil {
		cliflags.Fatalf("listing notification templates: %v", err)
	}

	if flags.JSON {
		cliflags.MustPrintJSON(templates)
		return
	}
	if len(templates) == 0 {
		cliflags.Print("No templatable events.")
		return
	}

	if !showBody {
		table := cliflags.NewTable("EVENT", "SOURCE", "TITLE", "UPDATED")
		for _, entry := range templates {
			table.Row(entry.EventType, templateSource(entry.Customized),
				cliflags.Truncate(entry.Title, 40), cliflags.FormatTime(entry.UpdatedAt))
		}
		table.Flush()
		return
	}

	for i, entry := range templates {
		if i > 0 {
			cliflags.Print("")
		}
		cliflags.Print("%s (%s)", entry.EventType, templateSource(entry.Customized))
		cliflags.Print("  Title: %s", entry.Title)
		cliflags.Print("  Body:  %s", entry.Body)
		if len(entry.Placeholders) > 0 {
			cliflags.Print("  Placeholders: %s", strings.Join(entry.Placeholders, ", "))
		}
	}
}

func templateSource(customized bool) string {
	if customized {
		return "customized"
	}
	return "default"
}

func runSetNotificationTemplate(event, title, body string) {
	tmpl, err := api().PutNotificationTemplate(ctx(), event, sdk.NotificationTemplate{
		Title: title,
		Body:  body,
	})
	if err != nil {
		cliflags.Fatalf("setting template: %v", err)
	}

	if flags.JSON {
		cliflags.MustPrintJSON(tmpl)
		return
	}
	cliflags.Print("✓ Copy for %s updated", event)
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
