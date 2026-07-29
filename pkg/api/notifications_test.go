package api

import (
	"reflect"
	"testing"

	"github.com/zrougamed/orion-belt/pkg/common"
)

// An unknown channel in an admin payload must be reported rather than silently
// dropped — a typo that looks accepted would enforce nothing.
func TestUnknownChannelsRejectsTypos(t *testing.T) {
	policy := &common.NotificationPolicy{
		AllowedChannels: []string{common.ChannelInApp, "in-app"},
		MandatoryEvents: map[string][]string{
			"access_request.approved": {"slack_dm"},
		},
	}

	got := unknownChannels(policy)
	if len(got) != 2 {
		t.Fatalf("expected both unknown channels, got %v", got)
	}
	if !contains(got, "in-app") || !contains(got, "slack_dm") {
		t.Errorf("expected in-app and slack_dm to be flagged, got %v", got)
	}
}

func TestUnknownChannelsAcceptsValidPolicy(t *testing.T) {
	policy := &common.NotificationPolicy{
		AllowedChannels: []string{common.ChannelInApp, " EMAIL "},
		MandatoryEvents: map[string][]string{
			"access_request.approved": {common.ChannelInApp},
		},
	}

	if got := unknownChannels(policy); got != nil {
		t.Errorf("expected no unknown channels, got %v", got)
	}
}

func TestUnknownChannelsDeduplicates(t *testing.T) {
	policy := &common.NotificationPolicy{
		AllowedChannels: []string{"pigeon", "pigeon"},
		MandatoryEvents: map[string][]string{
			"a": {"pigeon"},
			"b": {"pigeon"},
		},
	}

	if got := unknownChannels(policy); !reflect.DeepEqual(got, []string{"pigeon"}) {
		t.Errorf("expected a single de-duplicated entry, got %v", got)
	}
}

// The prefs response has to carry the bounds, so a client can render locked
// controls without a second request to the admin-only policy endpoint.
func TestNewPrefsResponseCarriesBounds(t *testing.T) {
	s := &APIServer{}
	policy := &common.NotificationPolicy{
		AllowedChannels: []string{common.ChannelInApp},
		MandatoryEvents: map[string][]string{
			"access_request.approved": {common.ChannelInApp},
		},
	}
	prefs := &common.NotificationPrefs{UserID: "u1", InAppEnabled: true}

	resp := s.newPrefsResponse(prefs, policy, true)

	if !reflect.DeepEqual(resp.AllowedChannels, []string{common.ChannelInApp}) {
		t.Errorf("expected only in_app allowed, got %v", resp.AllowedChannels)
	}
	if !reflect.DeepEqual(resp.MandatoryEvents[common.ChannelInApp], []string{"access_request.approved"}) {
		t.Errorf("expected the mandatory event surfaced, got %v", resp.MandatoryEvents)
	}
	if !resp.Adjusted {
		t.Error("expected Adjusted to be reported to the caller")
	}
	if resp.NotificationPrefs != prefs {
		t.Error("expected the user's preferences to be embedded in the response")
	}
}

// With no policy configured, the response must advertise every known channel
// and no locks, matching pre-policy behavior.
func TestNewPrefsResponseDefaultPolicy(t *testing.T) {
	s := &APIServer{}
	prefs := common.DefaultNotificationPrefs("u1")

	resp := s.newPrefsResponse(prefs, common.DefaultNotificationPolicy(), false)

	if !reflect.DeepEqual(resp.AllowedChannels, common.KnownNotificationChannels()) {
		t.Errorf("expected all known channels, got %v", resp.AllowedChannels)
	}
	if resp.MandatoryEvents != nil {
		t.Errorf("expected no mandatory events, got %v", resp.MandatoryEvents)
	}
	if resp.Adjusted {
		t.Error("expected no adjustment to be reported")
	}
}

func contains(list []string, want string) bool {
	for _, v := range list {
		if v == want {
			return true
		}
	}
	return false
}
