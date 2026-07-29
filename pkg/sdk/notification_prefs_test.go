package sdk

import (
	"encoding/json"
	"reflect"
	"testing"
)

// Pins the wire shape of GET/PUT /notifications/prefs. The server embeds the
// preferences alongside the bounds, so the bounds must land on the outer struct
// while the preference fields flatten in from the embedded type — if either
// side is renamed, this fails rather than silently decoding zero values.
func TestNotificationPrefsResultUnmarshalsServerShape(t *testing.T) {
	payload := []byte(`{
		"user_id": "u1",
		"in_app_enabled": true,
		"email_enabled": false,
		"event_types": ["access_request.approved"],
		"allowed_channels": ["in_app"],
		"mandatory_events": {"in_app": ["access_request.approved"]},
		"adjusted": true
	}`)

	var got NotificationPrefsResult
	if err := json.Unmarshal(payload, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if got.UserID != "u1" {
		t.Errorf("embedded preference fields did not flatten: user_id = %q", got.UserID)
	}
	if !got.InAppEnabled || got.EmailEnabled {
		t.Errorf("unexpected channel toggles: in_app=%v email=%v", got.InAppEnabled, got.EmailEnabled)
	}
	if !reflect.DeepEqual(got.EventTypes, []string{"access_request.approved"}) {
		t.Errorf("event_types = %v", got.EventTypes)
	}
	if !reflect.DeepEqual(got.AllowedChannels, []string{"in_app"}) {
		t.Errorf("allowed_channels = %v", got.AllowedChannels)
	}
	if !reflect.DeepEqual(got.MandatoryEvents["in_app"], []string{"access_request.approved"}) {
		t.Errorf("mandatory_events = %v", got.MandatoryEvents)
	}
	if !got.Adjusted {
		t.Error("adjusted did not decode")
	}
}

// A server with no policy configured omits the optional fields; decoding must
// still succeed and simply report no locks.
func TestNotificationPrefsResultTolerantOfAbsentBounds(t *testing.T) {
	var got NotificationPrefsResult
	if err := json.Unmarshal([]byte(`{"user_id":"u1","in_app_enabled":true}`), &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got.Adjusted || got.MandatoryEvents != nil || got.AllowedChannels != nil {
		t.Errorf("expected empty bounds, got %+v", got)
	}
}

// The narrower NotificationPrefs return type stays usable against the same
// richer response, which is what keeps the original SDK methods non-breaking.
func TestNotificationPrefsIgnoresBoundsFields(t *testing.T) {
	payload := []byte(`{"user_id":"u1","in_app_enabled":true,"allowed_channels":["in_app"],"adjusted":true}`)

	var got NotificationPrefs
	if err := json.Unmarshal(payload, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got.UserID != "u1" || !got.InAppEnabled {
		t.Errorf("legacy decode broke: %+v", got)
	}
}

// The policy type is what admin callers send; its field names are the stored
// contract, so pin them too.
func TestNotificationPolicyRoundTrip(t *testing.T) {
	policy := NotificationPolicy{
		AllowedChannels: []string{"in_app"},
		MandatoryEvents: map[string][]string{"access_request.approved": {"in_app"}},
	}

	b, err := json.Marshal(policy)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var raw map[string]json.RawMessage
	if err := json.Unmarshal(b, &raw); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	for _, key := range []string{"allowed_channels", "mandatory_events"} {
		if _, ok := raw[key]; !ok {
			t.Errorf("expected %q on the wire, got %s", key, b)
		}
	}
}
