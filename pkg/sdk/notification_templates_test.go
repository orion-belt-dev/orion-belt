package sdk

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
)

// The list endpoint wraps entries in {"templates": [...]} and flattens the
// template's own fields into each entry alongside the default copy, so pin
// both halves — a rename on either side would otherwise decode to zero values.
func TestListNotificationTemplatesParsesServerShape(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/admin/notifications/templates" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"templates":[
			{
				"event_type": "access_request.approved",
				"title": "Access granted",
				"body": "Your request for {{machine}} was approved.",
				"updated_at": "2026-07-30T20:00:00Z",
				"customized": true,
				"default_title": "Access request approved",
				"default_body": "Your access request for {{machine}} was approved.",
				"placeholders": ["machine", "ttl"]
			},
			{
				"event_type": "access_request.rejected",
				"title": "Access request rejected",
				"body": "Your access request was rejected.",
				"customized": false,
				"default_title": "Access request rejected",
				"default_body": "Your access request was rejected.",
				"placeholders": ["machine"]
			}
		]}`))
	}))
	defer server.Close()

	client, err := NewClient(server.URL, WithAPIKey("k123"))
	if err != nil {
		t.Fatalf("NewClient error: %v", err)
	}

	templates, err := client.ListNotificationTemplates(context.Background())
	if err != nil {
		t.Fatalf("ListNotificationTemplates error: %v", err)
	}
	if len(templates) != 2 {
		t.Fatalf("want 2 templates, got %d", len(templates))
	}

	first := templates[0]
	if first.EventType != "access_request.approved" {
		t.Errorf("embedded template fields did not flatten: event_type = %q", first.EventType)
	}
	if first.Title != "Access granted" {
		t.Errorf("title = %q", first.Title)
	}
	if !first.Customized {
		t.Error("customized did not decode")
	}
	if first.DefaultTitle != "Access request approved" {
		t.Errorf("default_title = %q", first.DefaultTitle)
	}
	if !reflect.DeepEqual(first.Placeholders, []string{"machine", "ttl"}) {
		t.Errorf("placeholders = %v", first.Placeholders)
	}
	if first.UpdatedAt.IsZero() {
		t.Error("updated_at did not decode")
	}

	// An event still on its built-in copy reports no override and, because
	// the server omits a zero timestamp, no update time.
	if templates[1].Customized {
		t.Error("second template should not be marked customized")
	}
	if !templates[1].UpdatedAt.IsZero() {
		t.Errorf("an uncustomized template should carry no update time, got %v", templates[1].UpdatedAt)
	}
}

// An empty template set decodes to an empty slice rather than nil, so callers
// can range over the result without a nil check.
func TestListNotificationTemplatesNeverReturnsNil(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"templates":null}`))
	}))
	defer server.Close()

	client, err := NewClient(server.URL, WithAPIKey("k123"))
	if err != nil {
		t.Fatalf("NewClient error: %v", err)
	}

	templates, err := client.ListNotificationTemplates(context.Background())
	if err != nil {
		t.Fatalf("ListNotificationTemplates error: %v", err)
	}
	if templates == nil {
		t.Fatal("want an empty slice, got nil")
	}
	if len(templates) != 0 {
		t.Errorf("want no templates, got %d", len(templates))
	}
}

// The event named in the path is authoritative, and the client sets it on the
// body too so the server's path/body agreement check passes.
func TestPutNotificationTemplateSetsEventFromPath(t *testing.T) {
	var received NotificationTemplate

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPut {
			t.Errorf("unexpected method: %s", r.Method)
		}
		if r.URL.Path != "/api/v1/admin/notifications/templates/access_request.approved" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		if err := json.NewDecoder(r.Body).Decode(&received); err != nil {
			t.Errorf("decode: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"event_type":"access_request.approved","title":"Access granted","body":"Approved."}`))
	}))
	defer server.Close()

	client, err := NewClient(server.URL, WithAPIKey("k123"))
	if err != nil {
		t.Fatalf("NewClient error: %v", err)
	}

	// Deliberately leave EventType unset: the path must supply it.
	stored, err := client.PutNotificationTemplate(context.Background(), "access_request.approved",
		NotificationTemplate{Title: "Access granted", Body: "Approved."})
	if err != nil {
		t.Fatalf("PutNotificationTemplate error: %v", err)
	}
	if received.EventType != "access_request.approved" {
		t.Errorf("server received event_type = %q, want it filled in from the path", received.EventType)
	}
	if received.Title != "Access granted" || received.Body != "Approved." {
		t.Errorf("server received %+v", received)
	}
	if stored.Title != "Access granted" {
		t.Errorf("stored template = %+v", stored)
	}
}

// Event types are path segments, so one containing a character that needs
// escaping must not break out of the path.
func TestPutNotificationTemplateEscapesEventName(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.Contains(r.RequestURI, "weird%2Fevent") {
			t.Errorf("expected an escaped event in the request URI: %s", r.RequestURI)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{}`))
	}))
	defer server.Close()

	client, err := NewClient(server.URL, WithAPIKey("k123"))
	if err != nil {
		t.Fatalf("NewClient error: %v", err)
	}

	if _, err := client.PutNotificationTemplate(context.Background(), "weird/event",
		NotificationTemplate{Title: "t", Body: "b"}); err != nil {
		t.Fatalf("PutNotificationTemplate error: %v", err)
	}
}

func TestDeleteNotificationTemplate(t *testing.T) {
	called := false

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		if r.Method != http.MethodDelete {
			t.Errorf("unexpected method: %s", r.Method)
		}
		if r.URL.Path != "/api/v1/admin/notifications/templates/access_request.expired" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	client, err := NewClient(server.URL, WithAPIKey("k123"))
	if err != nil {
		t.Fatalf("NewClient error: %v", err)
	}

	if err := client.DeleteNotificationTemplate(context.Background(), "access_request.expired"); err != nil {
		t.Fatalf("DeleteNotificationTemplate error: %v", err)
	}
	if !called {
		t.Error("the request never reached the server")
	}
}

// Gateway info is served off the versioned root rather than under /api/v1
// like the rest of the client surface, and needs no credentials.
func TestGetGatewayInfoIsUnauthenticated(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/gateway-info" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		if r.Header.Get("X-API-Key") != "" {
			t.Error("gateway info should not send credentials")
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"public_url": "https://pam.example.com",
			"ui_url": "https://pam.example.com/ui",
			"ssh_host": "pam.example.com",
			"ssh_port": 2222,
			"api_port": 8080
		}`))
	}))
	defer server.Close()

	client, err := NewClient(server.URL, WithAPIKey("k123"))
	if err != nil {
		t.Fatalf("NewClient error: %v", err)
	}

	info, err := client.GetGatewayInfo(context.Background())
	if err != nil {
		t.Fatalf("GetGatewayInfo error: %v", err)
	}
	if info.PublicURL != "https://pam.example.com" || info.UIURL != "https://pam.example.com/ui" {
		t.Errorf("unexpected URLs: %+v", info)
	}
	if info.SSHHost != "pam.example.com" || info.SSHPort != 2222 || info.APIPort != 8080 {
		t.Errorf("unexpected addresses: %+v", info)
	}
	// Absent overrides stay empty rather than decoding as zero-value noise.
	if info.PublicSSHHost != "" || info.PublicSSHPort != 0 {
		t.Errorf("expected no configured override, got %+v", info)
	}
}
