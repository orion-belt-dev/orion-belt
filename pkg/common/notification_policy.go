package common

import (
	"sort"
	"strings"
	"time"
)

// Notification delivery channels. A channel is a distinct way a single user can
// be reached; broadcast destinations (the shared Slack webhook, the operator
// `to:` list on the email plugin) are not channels in this sense because they
// are not addressed per user and so cannot carry a per-user preference.
const (
	ChannelInApp = "in_app"
	ChannelEmail = "email"
)

// KnownNotificationChannels lists every channel a policy or preference may
// name, in stable display order. Adding a channel here is what makes it
// selectable by users and governable by admins.
func KnownNotificationChannels() []string {
	return []string{ChannelInApp, ChannelEmail}
}

// IsKnownNotificationChannel reports whether channel is one this build can
// reason about. Unknown channels are rejected on write rather than stored,
// so a typo in an admin payload can't silently become an unenforceable rule.
func IsKnownNotificationChannel(channel string) bool {
	for _, c := range KnownNotificationChannels() {
		if c == channel {
			return true
		}
	}
	return false
}

// NotificationPolicy is the admin-set boundary that user preferences are
// resolved within. It is global (not per-user) and there is at most one row.
//
// The zero value is deliberately permissive — it reproduces the behavior that
// existed before policies were introduced, so an install that never configures
// one sees no change.
type NotificationPolicy struct {
	// AllowedChannels restricts which channels users may enable for
	// themselves. Empty means "every known channel", not "none" — an empty
	// list is the default state and must not lock everyone out.
	AllowedChannels []string `json:"allowed_channels"`

	// MandatoryEvents maps an event type to the channels on which it is
	// always delivered, regardless of what the recipient prefers. This is the
	// escape hatch for notifications an operator is required to guarantee
	// (break-glass access, credential changes) and is the one thing a user
	// preference cannot switch off.
	MandatoryEvents map[string][]string `json:"mandatory_events"`

	UpdatedAt time.Time `json:"updated_at"`
}

// DefaultNotificationPolicy returns the permissive default: every known channel
// is selectable and nothing is forced.
func DefaultNotificationPolicy() *NotificationPolicy {
	return &NotificationPolicy{
		AllowedChannels: nil,
		MandatoryEvents: map[string][]string{},
	}
}

// ChannelAllowed reports whether users may enable channel for themselves.
// A nil policy or an empty AllowedChannels list allows every known channel.
func (p *NotificationPolicy) ChannelAllowed(channel string) bool {
	if !IsKnownNotificationChannel(channel) {
		return false
	}
	if p == nil || len(p.AllowedChannels) == 0 {
		return true
	}
	for _, c := range p.AllowedChannels {
		if c == channel {
			return true
		}
	}
	return false
}

// EffectiveAllowedChannels expands the policy into the concrete channel list a
// user may choose from, so callers (API, UI) never have to re-implement the
// "empty means all" rule.
func (p *NotificationPolicy) EffectiveAllowedChannels() []string {
	if p == nil || len(p.AllowedChannels) == 0 {
		return KnownNotificationChannels()
	}
	out := make([]string, 0, len(p.AllowedChannels))
	for _, c := range KnownNotificationChannels() {
		if p.ChannelAllowed(c) {
			out = append(out, c)
		}
	}
	return out
}

// MandatoryChannelsFor returns the channels on which eventType must always be
// delivered, in stable order.
func (p *NotificationPolicy) MandatoryChannelsFor(eventType string) []string {
	if p == nil || len(p.MandatoryEvents) == 0 {
		return nil
	}
	channels := p.MandatoryEvents[eventType]
	if len(channels) == 0 {
		return nil
	}
	out := make([]string, 0, len(channels))
	for _, c := range KnownNotificationChannels() {
		for _, m := range channels {
			if m == c {
				out = append(out, c)
				break
			}
		}
	}
	return out
}

// IsMandatory reports whether eventType must be delivered on channel no matter
// what the recipient has configured.
func (p *NotificationPolicy) IsMandatory(channel, eventType string) bool {
	for _, c := range p.MandatoryChannelsFor(eventType) {
		if c == channel {
			return true
		}
	}
	return false
}

// MandatoryEventsForChannel returns the event types pinned on to channel, in
// stable order. The UI uses this to render those events as locked.
func (p *NotificationPolicy) MandatoryEventsForChannel(channel string) []string {
	if p == nil || len(p.MandatoryEvents) == 0 {
		return nil
	}
	out := make([]string, 0, len(p.MandatoryEvents))
	for event := range p.MandatoryEvents {
		if p.IsMandatory(channel, event) {
			out = append(out, event)
		}
	}
	sort.Strings(out)
	return out
}

// Deliver is the single decision point for "should this notification go out on
// this channel for this user". Keeping the precedence in one place is what
// stops the two acceptance rules from drifting apart across call sites.
//
// Precedence, highest first:
//  1. Admin-mandated events always deliver.
//  2. A channel the admin disallows never delivers.
//  3. Otherwise the user's own preference decides.
func (p *NotificationPolicy) Deliver(prefs *NotificationPrefs, channel, eventType string) bool {
	if p.IsMandatory(channel, eventType) {
		return true
	}
	if !p.ChannelAllowed(channel) {
		return false
	}
	return prefs.Allows(channel, eventType)
}

// ClampPrefs rewrites prefs in place so they sit inside the policy, and reports
// whether anything had to change. This runs on save so that what we persist is
// already the effective truth — no call site has to remember to re-apply bounds
// when reading, and the user is shown back exactly what will happen.
//
// Two corrections are applied:
//   - a channel the admin disallows is switched off;
//   - a mandatory event has its channel switched on and is added to the event
//     allow-list, so a narrow allow-list cannot filter it out.
func (p *NotificationPolicy) ClampPrefs(prefs *NotificationPrefs) bool {
	if prefs == nil {
		return false
	}
	changed := false

	for _, channel := range KnownNotificationChannels() {
		enabled := prefs.channelEnabled(channel)

		// A disallowed channel is forced off, unless something mandatory
		// rides on it — rule 1 outranks rule 2 (see Deliver).
		if enabled && !p.ChannelAllowed(channel) && len(p.MandatoryEventsForChannel(channel)) == 0 {
			prefs.setChannelEnabled(channel, false)
			changed = true
			continue
		}

		// A channel carrying mandatory events must be on, or those events
		// would be silently dropped at delivery time.
		if !enabled && len(p.MandatoryEventsForChannel(channel)) > 0 {
			prefs.setChannelEnabled(channel, true)
			changed = true
		}
	}

	// An explicit allow-list that omits a mandatory event would filter it out,
	// so fold those events back in. An empty list already means "all events"
	// and needs no repair.
	if len(prefs.EventTypes) > 0 {
		for _, channel := range KnownNotificationChannels() {
			for _, event := range p.MandatoryEventsForChannel(channel) {
				if !containsString(prefs.EventTypes, event) {
					prefs.EventTypes = append(prefs.EventTypes, event)
					changed = true
				}
			}
		}
	}

	return changed
}

// Normalize cleans an admin-supplied policy before it is stored: unknown and
// duplicate channels are dropped, empty event entries are removed, and any
// mandatory channel is promoted into AllowedChannels so the stored policy can
// never contradict itself.
func (p *NotificationPolicy) Normalize() {
	if p == nil {
		return
	}

	p.AllowedChannels = normalizeChannels(p.AllowedChannels)

	if len(p.MandatoryEvents) == 0 {
		p.MandatoryEvents = map[string][]string{}
		return
	}

	cleaned := make(map[string][]string, len(p.MandatoryEvents))
	for event, channels := range p.MandatoryEvents {
		event = strings.TrimSpace(event)
		if event == "" {
			continue
		}
		normalized := normalizeChannels(channels)
		if len(normalized) == 0 {
			continue
		}
		cleaned[event] = normalized
	}
	p.MandatoryEvents = cleaned

	// Promote mandatory channels into the allow-list. Without this an admin
	// could store "email is not selectable" alongside "this event is mandatory
	// on email", and the contradiction would only surface at delivery time.
	if len(p.AllowedChannels) > 0 {
		for _, channels := range cleaned {
			for _, c := range channels {
				if !containsString(p.AllowedChannels, c) {
					p.AllowedChannels = append(p.AllowedChannels, c)
				}
			}
		}
		p.AllowedChannels = normalizeChannels(p.AllowedChannels)
	}
}

// normalizeChannels lowercases, de-duplicates, and drops unknown channels,
// returning them in the canonical order of KnownNotificationChannels.
func normalizeChannels(channels []string) []string {
	if len(channels) == 0 {
		return nil
	}
	seen := make(map[string]bool, len(channels))
	for _, c := range channels {
		c = strings.ToLower(strings.TrimSpace(c))
		if IsKnownNotificationChannel(c) {
			seen[c] = true
		}
	}
	if len(seen) == 0 {
		return nil
	}
	out := make([]string, 0, len(seen))
	for _, c := range KnownNotificationChannels() {
		if seen[c] {
			out = append(out, c)
		}
	}
	return out
}

func containsString(list []string, want string) bool {
	for _, v := range list {
		if v == want {
			return true
		}
	}
	return false
}
