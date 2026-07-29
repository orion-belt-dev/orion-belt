package common

import (
	"reflect"
	"testing"
)

const (
	eventApproved = "access_request.approved"
	eventRejected = "access_request.rejected"
	eventExpired  = "access_request.expired"
)

// An install with no configured policy must behave exactly as it did before
// policies existed: every known channel selectable, nothing forced.
func TestDefaultPolicyIsPermissive(t *testing.T) {
	policy := DefaultNotificationPolicy()

	for _, channel := range KnownNotificationChannels() {
		if !policy.ChannelAllowed(channel) {
			t.Errorf("default policy should allow channel %q", channel)
		}
		if policy.IsMandatory(channel, eventApproved) {
			t.Errorf("default policy should mandate nothing on %q", channel)
		}
	}

	if got := policy.EffectiveAllowedChannels(); !reflect.DeepEqual(got, KnownNotificationChannels()) {
		t.Errorf("expected all known channels, got %v", got)
	}
}

// A nil policy is what callers see when the lookup is skipped entirely; it must
// not accidentally deny everything.
func TestNilPolicyAllowsKnownChannels(t *testing.T) {
	var policy *NotificationPolicy

	if !policy.ChannelAllowed(ChannelInApp) {
		t.Error("nil policy should allow in_app")
	}
	if policy.ChannelAllowed("carrier_pigeon") {
		t.Error("nil policy should still reject unknown channels")
	}
	if policy.IsMandatory(ChannelInApp, eventApproved) {
		t.Error("nil policy should mandate nothing")
	}
}

// AC1: a channel the admin has not allowed must not deliver, even when the
// user has switched it on.
func TestDisallowedChannelDoesNotDeliver(t *testing.T) {
	policy := &NotificationPolicy{AllowedChannels: []string{ChannelInApp}}
	prefs := &NotificationPrefs{InAppEnabled: true, EmailEnabled: true}

	if !policy.Deliver(prefs, ChannelInApp, eventApproved) {
		t.Error("in_app is allowed and enabled, expected delivery")
	}
	if policy.Deliver(prefs, ChannelEmail, eventApproved) {
		t.Error("email is not in AllowedChannels, expected no delivery")
	}
}

// AC1: saving preferences must fold them into the admin bounds.
func TestClampPrefsDisablesDisallowedChannel(t *testing.T) {
	policy := &NotificationPolicy{AllowedChannels: []string{ChannelInApp}}
	prefs := &NotificationPrefs{UserID: "u1", InAppEnabled: true, EmailEnabled: true}

	if !policy.ClampPrefs(prefs) {
		t.Fatal("expected ClampPrefs to report a correction")
	}
	if prefs.EmailEnabled {
		t.Error("email should have been switched off by the policy")
	}
	if !prefs.InAppEnabled {
		t.Error("in_app is allowed and should have been left on")
	}
}

// Clamping must be a no-op when the preferences already fit, so an unchanged
// save does not report a spurious adjustment to the user.
func TestClampPrefsLeavesCompliantPrefsAlone(t *testing.T) {
	policy := &NotificationPolicy{AllowedChannels: []string{ChannelInApp, ChannelEmail}}
	prefs := &NotificationPrefs{UserID: "u1", InAppEnabled: true, EventTypes: []string{eventApproved}}

	if policy.ClampPrefs(prefs) {
		t.Error("compliant prefs should not be reported as adjusted")
	}
	if !reflect.DeepEqual(prefs.EventTypes, []string{eventApproved}) {
		t.Errorf("event list should be untouched, got %v", prefs.EventTypes)
	}
}

// AC2: the headline rule — a user who has turned the channel off entirely
// still receives an admin-mandated event.
func TestMandatoryEventDeliversDespiteDisabledChannel(t *testing.T) {
	policy := &NotificationPolicy{
		MandatoryEvents: map[string][]string{eventApproved: {ChannelInApp}},
	}
	prefs := &NotificationPrefs{InAppEnabled: false, EmailEnabled: false}

	if !policy.Deliver(prefs, ChannelInApp, eventApproved) {
		t.Error("mandatory event must deliver even with the channel disabled")
	}
	// The mandate is scoped to the event it names, not to the whole channel.
	if policy.Deliver(prefs, ChannelInApp, eventRejected) {
		t.Error("non-mandatory event should still respect the disabled channel")
	}
}

// AC2: a narrow event allow-list must not be able to filter out a mandated
// event either — the other way a user could switch one off.
func TestMandatoryEventDeliversDespiteNarrowAllowList(t *testing.T) {
	policy := &NotificationPolicy{
		MandatoryEvents: map[string][]string{eventApproved: {ChannelInApp}},
	}
	prefs := &NotificationPrefs{InAppEnabled: true, EventTypes: []string{eventRejected}}

	if !policy.Deliver(prefs, ChannelInApp, eventApproved) {
		t.Error("mandatory event must deliver despite an allow-list that omits it")
	}
}

// AC2: mandatory outranks a disallowed channel. This combination is prevented
// on write by Normalize, but delivery must not depend on that having run.
func TestMandatoryOutranksDisallowedChannel(t *testing.T) {
	policy := &NotificationPolicy{
		AllowedChannels: []string{ChannelInApp},
		MandatoryEvents: map[string][]string{eventApproved: {ChannelEmail}},
	}
	prefs := &NotificationPrefs{InAppEnabled: true, EmailEnabled: false}

	if !policy.Deliver(prefs, ChannelEmail, eventApproved) {
		t.Error("an explicitly mandated event must win over the allow-list")
	}
}

// Clamping must repair both ways a user could have suppressed a mandated
// event, so the stored preferences match what delivery will actually do.
func TestClampPrefsRestoresMandatoryEvent(t *testing.T) {
	policy := &NotificationPolicy{
		MandatoryEvents: map[string][]string{eventApproved: {ChannelInApp}},
	}
	prefs := &NotificationPrefs{
		UserID:       "u1",
		InAppEnabled: false,
		EventTypes:   []string{eventRejected},
	}

	if !policy.ClampPrefs(prefs) {
		t.Fatal("expected ClampPrefs to report a correction")
	}
	if !prefs.InAppEnabled {
		t.Error("channel carrying a mandatory event must be switched back on")
	}
	if !containsString(prefs.EventTypes, eventApproved) {
		t.Errorf("mandatory event must be folded into the allow-list, got %v", prefs.EventTypes)
	}
	// The user's own selection must survive the repair.
	if !containsString(prefs.EventTypes, eventRejected) {
		t.Errorf("user's own event selection was lost, got %v", prefs.EventTypes)
	}
}

// An empty allow-list already means "all events", so clamping must not
// materialize a list and thereby narrow the user's preferences.
func TestClampPrefsLeavesEmptyAllowListEmpty(t *testing.T) {
	policy := &NotificationPolicy{
		MandatoryEvents: map[string][]string{eventApproved: {ChannelInApp}},
	}
	prefs := &NotificationPrefs{UserID: "u1", InAppEnabled: true, EventTypes: nil}

	policy.ClampPrefs(prefs)

	if len(prefs.EventTypes) != 0 {
		t.Errorf("empty allow-list means all events and should stay empty, got %v", prefs.EventTypes)
	}
	if !policy.Deliver(prefs, ChannelInApp, eventExpired) {
		t.Error("empty allow-list should still deliver every event")
	}
}

// Normalize is what stops an admin from storing a policy that contradicts
// itself: mandating a channel they simultaneously made unselectable.
func TestNormalizePromotesMandatoryChannelIntoAllowList(t *testing.T) {
	policy := &NotificationPolicy{
		AllowedChannels: []string{ChannelInApp},
		MandatoryEvents: map[string][]string{eventApproved: {ChannelEmail}},
	}

	policy.Normalize()

	if !policy.ChannelAllowed(ChannelEmail) {
		t.Error("a mandated channel must be promoted into the allow-list")
	}
}

func TestNormalizeDropsUnknownAndDuplicateChannels(t *testing.T) {
	policy := &NotificationPolicy{
		AllowedChannels: []string{"  IN_APP ", ChannelInApp, "carrier_pigeon"},
		MandatoryEvents: map[string][]string{
			eventApproved: {"carrier_pigeon"}, // no known channels left -> dropped
			"  ":          {ChannelInApp},     // blank event -> dropped
			eventRejected: {ChannelInApp},
		},
	}

	policy.Normalize()

	if !reflect.DeepEqual(policy.AllowedChannels, []string{ChannelInApp}) {
		t.Errorf("expected de-duplicated [in_app], got %v", policy.AllowedChannels)
	}
	if _, ok := policy.MandatoryEvents[eventApproved]; ok {
		t.Error("an entry with no known channels should be dropped")
	}
	if len(policy.MandatoryEvents) != 1 {
		t.Errorf("expected only the valid entry to survive, got %v", policy.MandatoryEvents)
	}
}

// Ordering is stable regardless of how the admin submitted the policy, so the
// UI and API responses do not shuffle between reads.
func TestMandatoryChannelsForIsStablyOrdered(t *testing.T) {
	policy := &NotificationPolicy{
		MandatoryEvents: map[string][]string{
			eventApproved: {ChannelEmail, ChannelInApp},
		},
	}

	got := policy.MandatoryChannelsFor(eventApproved)
	if !reflect.DeepEqual(got, []string{ChannelInApp, ChannelEmail}) {
		t.Errorf("expected canonical channel order, got %v", got)
	}
	if policy.MandatoryChannelsFor("no.such.event") != nil {
		t.Error("expected nil for an event with no mandate")
	}
}

func TestMandatoryEventsForChannel(t *testing.T) {
	policy := &NotificationPolicy{
		MandatoryEvents: map[string][]string{
			eventRejected: {ChannelInApp},
			eventApproved: {ChannelInApp},
			eventExpired:  {ChannelEmail},
		},
	}

	got := policy.MandatoryEventsForChannel(ChannelInApp)
	if !reflect.DeepEqual(got, []string{eventApproved, eventRejected}) {
		t.Errorf("expected sorted in_app events, got %v", got)
	}
}

// Preference resolution on its own, independent of any policy.
func TestPrefsAllows(t *testing.T) {
	tests := []struct {
		name    string
		prefs   *NotificationPrefs
		channel string
		event   string
		want    bool
	}{
		{"nil prefs fall back to defaults on in_app", nil, ChannelInApp, eventApproved, true},
		{"nil prefs do not opt into email", nil, ChannelEmail, eventApproved, false},
		{"disabled channel", &NotificationPrefs{}, ChannelInApp, eventApproved, false},
		{"enabled, empty list means all", &NotificationPrefs{InAppEnabled: true}, ChannelInApp, eventApproved, true},
		{
			"enabled, event in list",
			&NotificationPrefs{InAppEnabled: true, EventTypes: []string{eventApproved}},
			ChannelInApp, eventApproved, true,
		},
		{
			"enabled, event not in list",
			&NotificationPrefs{InAppEnabled: true, EventTypes: []string{eventRejected}},
			ChannelInApp, eventApproved, false,
		},
		{"unknown channel is never deliverable", &NotificationPrefs{InAppEnabled: true}, "carrier_pigeon", eventApproved, false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.prefs.Allows(tc.channel, tc.event); got != tc.want {
				t.Errorf("Allows(%q, %q) = %v, want %v", tc.channel, tc.event, got, tc.want)
			}
		})
	}
}

// AllowsInApp is published SDK surface; it must keep delegating to the same
// logic rather than drifting into a second implementation.
func TestAllowsInAppMatchesAllows(t *testing.T) {
	prefs := &NotificationPrefs{InAppEnabled: true, EventTypes: []string{eventApproved}}

	if prefs.AllowsInApp(eventApproved) != prefs.Allows(ChannelInApp, eventApproved) {
		t.Error("AllowsInApp diverged from Allows")
	}
	if prefs.AllowsInApp(eventRejected) {
		t.Error("expected an event outside the allow-list to be denied")
	}

	var nilPrefs *NotificationPrefs
	if !nilPrefs.AllowsInApp(eventApproved) {
		t.Error("nil prefs previously allowed in-app delivery; that must not change")
	}
}
