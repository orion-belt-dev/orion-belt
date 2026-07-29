package common

// NotificationPrefs stores per-user channel toggles for event types.
//
// These are the user's wishes, not the final answer: they are always resolved
// against the admin NotificationPolicy before anything is delivered. See
// NotificationPolicy.Deliver.
type NotificationPrefs struct {
	UserID       string `json:"user_id"`
	InAppEnabled bool   `json:"in_app_enabled"`
	EmailEnabled bool   `json:"email_enabled"`
	// EventTypes: empty = all; otherwise allow-list of notification types.
	// The list spans every channel the user has enabled.
	EventTypes []string `json:"event_types"`
}

// DefaultNotificationPrefs returns sensible defaults (in-app on, email off).
func DefaultNotificationPrefs(userID string) *NotificationPrefs {
	return &NotificationPrefs{
		UserID:       userID,
		InAppEnabled: true,
		EmailEnabled: false,
		EventTypes:   nil,
	}
}

// Allows reports whether the user wants eventType on channel. It is the
// preference half of the decision only — callers that deliver notifications
// must go through NotificationPolicy.Deliver so admin bounds and mandatory
// events are applied too.
func (p *NotificationPrefs) Allows(channel, eventType string) bool {
	if p == nil {
		// No stored preferences: fall back to the defaults a new user gets,
		// rather than silently opting them into every channel.
		return DefaultNotificationPrefs("").Allows(channel, eventType)
	}
	if !p.channelEnabled(channel) {
		return false
	}
	if len(p.EventTypes) == 0 {
		return true
	}
	return containsString(p.EventTypes, eventType)
}

// AllowsInApp reports whether eventType should be delivered on the in-app
// channel, ignoring admin policy.
//
// Deprecated: use NotificationPolicy.Deliver, which applies admin bounds and
// mandatory events on top of the user's preference. Retained because it is part
// of the published SDK surface (pkg/sdk aliases NotificationPrefs).
func (p *NotificationPrefs) AllowsInApp(eventType string) bool {
	return p.Allows(ChannelInApp, eventType)
}

// channelEnabled reads the toggle for a channel. Unknown channels are off —
// a channel this build doesn't implement must never be treated as deliverable.
func (p *NotificationPrefs) channelEnabled(channel string) bool {
	if p == nil {
		return false
	}
	switch channel {
	case ChannelInApp:
		return p.InAppEnabled
	case ChannelEmail:
		return p.EmailEnabled
	default:
		return false
	}
}

func (p *NotificationPrefs) setChannelEnabled(channel string, enabled bool) {
	if p == nil {
		return
	}
	switch channel {
	case ChannelInApp:
		p.InAppEnabled = enabled
	case ChannelEmail:
		p.EmailEnabled = enabled
	}
}
