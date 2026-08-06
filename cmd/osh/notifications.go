package main

import (
	"strings"

	"github.com/orion-belt-dev/orion-belt/pkg/cliflags"
	"github.com/orion-belt-dev/orion-belt/pkg/sdk"
	"github.com/spf13/cobra"
)

func newNotificationsCmd() *cobra.Command {
	notifications := &cobra.Command{
		Use:     "notifications",
		Aliases: []string{"notifs"},
		Short:   "Read your notifications and set delivery preferences",
		Long: `Read the in-app notifications addressed to you, mark them read, and choose
which channels and events you want.

Preferences are resolved against an admin policy, so a channel your operator
disallows cannot be enabled and a mandatory event cannot be switched off; the
prefs output shows those bounds.`,
	}

	var (
		limit  int
		unread bool
	)
	list := &cobra.Command{
		Use:   "list",
		Short: "List your notifications, newest first",
		Args:  cobra.NoArgs,
		Run: func(cmd *cobra.Command, args []string) {
			runNotificationsList(limit, unread)
		},
	}
	list.Flags().IntVar(&limit, "limit", 20, "maximum number of notifications to return")
	list.Flags().BoolVar(&unread, "unread", false, "only notifications you have not read")
	notifications.AddCommand(list)

	var all bool
	read := &cobra.Command{
		Use:   "read [notification-id]",
		Short: "Mark notifications read",
		Long:  `Marks one notification read, or every notification with --all.`,
		Args:  cobra.MaximumNArgs(1),
		Run: func(cmd *cobra.Command, args []string) {
			runNotificationsRead(args, all)
		},
	}
	read.Flags().BoolVar(&all, "all", false, "mark every notification read")
	notifications.AddCommand(read)

	var (
		inApp    bool
		noInApp  bool
		email    bool
		noEmail  bool
		events   []string
		anyEvent bool
	)
	prefs := &cobra.Command{
		Use:   "prefs",
		Short: "Show or change your notification preferences",
		Long: `Prints your delivery preferences and the admin bounds applied to them.

With --set, the flags that follow are submitted as your new preferences:

  --in-app / --no-in-app      in-app notifications on or off
  --email / --no-email        email notifications on or off
  --event access_request.approved
                              limit delivery to these event types (repeatable)
  --all-events                deliver every event type`,
		Args: cobra.NoArgs,
		Run: func(cmd *cobra.Command, args []string) {
			if !cmd.Flags().Changed("set") {
				runNotificationPrefs()
				return
			}
			runSetNotificationPrefs(prefsInput{
				inApp:    inApp,
				noInApp:  noInApp,
				email:    email,
				noEmail:  noEmail,
				events:   events,
				anyEvent: anyEvent,
			})
		},
	}
	prefs.Flags().Bool("set", false, "apply the preference flags below instead of printing")
	prefs.Flags().BoolVar(&inApp, "in-app", false, "enable in-app notifications")
	prefs.Flags().BoolVar(&noInApp, "no-in-app", false, "disable in-app notifications")
	prefs.Flags().BoolVar(&email, "email", false, "enable email notifications")
	prefs.Flags().BoolVar(&noEmail, "no-email", false, "disable email notifications")
	prefs.Flags().StringArrayVar(&events, "event", nil, "event type to deliver (repeatable; default is every type)")
	prefs.Flags().BoolVar(&anyEvent, "all-events", false, "deliver every event type")
	notifications.AddCommand(prefs)

	return notifications
}

func runNotificationsList(limit int, unreadOnly bool) {
	client := api()

	notifications, err := client.ListNotifications(ctx(), limit)
	if err != nil {
		cliflags.Fatalf("listing notifications: %v", err)
	}

	if unreadOnly {
		filtered := notifications[:0]
		for _, notification := range notifications {
			if notification.ReadAt == nil {
				filtered = append(filtered, notification)
			}
		}
		notifications = filtered
	}

	if flags.JSON {
		cliflags.MustPrintJSON(notifications)
		return
	}
	if len(notifications) == 0 {
		cliflags.Print("No notifications.")
		return
	}

	// IDs are printed in full: "notifications read" takes one.
	table := cliflags.NewTable("ID", "WHEN", "TYPE", "TITLE", "READ")
	for _, notification := range notifications {
		read := "no"
		if notification.ReadAt != nil {
			read = "yes"
		}
		table.Row(notification.ID, cliflags.FormatTime(notification.CreatedAt),
			notification.Type, cliflags.Truncate(notification.Title, 40), read)
	}
	table.Flush()

	if count, err := client.UnreadNotificationCount(ctx()); err == nil {
		cliflags.Print("\n%d unread", count)
	}
}

func runNotificationsRead(args []string, all bool) {
	client := api()

	if all {
		if len(args) > 0 {
			cliflags.Fatalf("--all takes no notification ID")
		}
		if err := client.MarkAllNotificationsRead(ctx()); err != nil {
			cliflags.Fatalf("marking notifications read: %v", err)
		}
		cliflags.Print("✓ All notifications marked read")
		return
	}

	if len(args) == 0 {
		cliflags.Fatalf("pass a notification ID, or --all")
	}
	if err := client.MarkNotificationRead(ctx(), args[0]); err != nil {
		cliflags.Fatalf("marking notification read: %v", err)
	}
	cliflags.Print("✓ Notification %s marked read", args[0])
}

func runNotificationPrefs() {
	result, err := api().GetNotificationPrefsWithBounds(ctx())
	if err != nil {
		cliflags.Fatalf("getting preferences: %v", err)
	}

	if flags.JSON {
		cliflags.MustPrintJSON(result)
		return
	}
	printPrefs(result)
}

type prefsInput struct {
	inApp    bool
	noInApp  bool
	email    bool
	noEmail  bool
	events   []string
	anyEvent bool
}

func runSetNotificationPrefs(input prefsInput) {
	if input.inApp && input.noInApp {
		cliflags.Fatalf("--in-app and --no-in-app are mutually exclusive")
	}
	if input.email && input.noEmail {
		cliflags.Fatalf("--email and --no-email are mutually exclusive")
	}
	if input.anyEvent && len(input.events) > 0 {
		cliflags.Fatalf("--all-events and --event are mutually exclusive")
	}

	client := api()

	// The API replaces preferences wholesale, so start from what is stored and
	// change only the flags that were given.
	current, err := client.GetNotificationPrefs(ctx())
	if err != nil {
		cliflags.Fatalf("reading current preferences: %v", err)
	}

	prefs := *current
	if input.inApp {
		prefs.InAppEnabled = true
	}
	if input.noInApp {
		prefs.InAppEnabled = false
	}
	if input.email {
		prefs.EmailEnabled = true
	}
	if input.noEmail {
		prefs.EmailEnabled = false
	}
	if input.anyEvent {
		prefs.EventTypes = nil
	}
	if len(input.events) > 0 {
		prefs.EventTypes = input.events
	}

	result, err := client.PutNotificationPrefsWithBounds(ctx(), prefs)
	if err != nil {
		cliflags.Fatalf("saving preferences: %v", err)
	}

	if flags.JSON {
		cliflags.MustPrintJSON(result)
		return
	}
	cliflags.Print("✓ Preferences saved")
	if result.Adjusted {
		cliflags.Print("! Your operator's policy adjusted these values; the stored result is below.")
	}
	printPrefs(result)
}

func printPrefs(result *sdk.NotificationPrefsResult) {
	cliflags.Print("In-app: %s", cliflags.YesNo(result.InAppEnabled))
	cliflags.Print("Email:  %s", cliflags.YesNo(result.EmailEnabled))

	events := "all event types"
	if len(result.EventTypes) > 0 {
		events = strings.Join(result.EventTypes, ", ")
	}
	cliflags.Print("Events: %s", events)

	if len(result.AllowedChannels) > 0 {
		cliflags.Print("\nChannels your operator allows: %s", strings.Join(result.AllowedChannels, ", "))
	}
	for channel, mandatory := range result.MandatoryEvents {
		cliflags.Print("Always delivered on %s: %s", channel, strings.Join(mandatory, ", "))
	}
}
