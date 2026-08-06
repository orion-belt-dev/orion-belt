package sdk

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
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

// GetNotificationPolicy reads the admin endpoint and returns the policy
// alongside the channels and events this server build knows, which is what a
// caller needs to submit a policy the server will accept.
func TestGetNotificationPolicyParsesServerShape(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/admin/notifications/policy" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"policy": {"allowed_channels":["in_app"],"mandatory_events":{"access_request.approved":["in_app"]}},
			"known_channels": ["in_app","email"],
			"known_events": ["access_request.approved","access_request.rejected"]
		}`))
	}))
	defer server.Close()

	client, err := NewClient(server.URL, WithAPIKey("k123"))
	if err != nil {
		t.Fatalf("NewClient error: %v", err)
	}

	result, err := client.GetNotificationPolicy(context.Background())
	if err != nil {
		t.Fatalf("GetNotificationPolicy error: %v", err)
	}
	if !reflect.DeepEqual(result.Policy.AllowedChannels, []string{"in_app"}) {
		t.Errorf("allowed_channels = %v", result.Policy.AllowedChannels)
	}
	if !reflect.DeepEqual(result.KnownChannels, []string{"in_app", "email"}) {
		t.Errorf("known_channels = %v", result.KnownChannels)
	}
	if len(result.KnownEvents) != 2 {
		t.Errorf("known_events = %v", result.KnownEvents)
	}
}

// PutNotificationPolicy replaces the policy wholesale and returns what was
// stored after the server normalized it.
func TestPutNotificationPolicySendsTheWholePolicy(t *testing.T) {
	var received NotificationPolicy

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPut {
			t.Errorf("unexpected method: %s", r.Method)
		}
		if r.URL.Path != "/api/v1/admin/notifications/policy" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		if err := json.NewDecoder(r.Body).Decode(&received); err != nil {
			t.Errorf("decode: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"allowed_channels":["in_app"],"mandatory_events":{}}`))
	}))
	defer server.Close()

	client, err := NewClient(server.URL, WithAPIKey("k123"))
	if err != nil {
		t.Fatalf("NewClient error: %v", err)
	}

	stored, err := client.PutNotificationPolicy(context.Background(), NotificationPolicy{
		AllowedChannels: []string{"in_app"},
		MandatoryEvents: map[string][]string{"access_request.approved": {"in_app"}},
	})
	if err != nil {
		t.Fatalf("PutNotificationPolicy error: %v", err)
	}
	if !reflect.DeepEqual(received.AllowedChannels, []string{"in_app"}) {
		t.Errorf("server received allowed_channels = %v", received.AllowedChannels)
	}
	if !reflect.DeepEqual(received.MandatoryEvents["access_request.approved"], []string{"in_app"}) {
		t.Errorf("server received mandatory_events = %v", received.MandatoryEvents)
	}
	if !reflect.DeepEqual(stored.AllowedChannels, []string{"in_app"}) {
		t.Errorf("stored policy = %+v", stored)
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
