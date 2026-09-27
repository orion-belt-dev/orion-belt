package sdk

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestListCapabilitiesSendsScopeAndDecodes(t *testing.T) {
	var gotQuery string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/capabilities" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		gotQuery = r.URL.RawQuery
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"user_id": "u1",
			"scope": "all",
			"generated_at": "2026-09-27T10:00:00Z",
			"truncated": true,
			"capabilities": [{
				"id": "m1:root:ssh",
				"machine_id": "m1",
				"machine_name": "web-01",
				"machine_active": true,
				"remote_user": "root",
				"access_type": "ssh",
				"status": "held",
				"source": "grant",
				"reason": "granted by admin",
				"expires_at": "2026-09-28T10:00:00Z"
			}]
		}`))
	}))
	defer server.Close()

	client, err := NewClient(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	got, err := client.ListCapabilities(context.Background(), "all", "u2")
	if err != nil {
		t.Fatalf("ListCapabilities: %v", err)
	}
	if gotQuery != "scope=all&user_id=u2" {
		t.Errorf("query = %q, want scope=all&user_id=u2", gotQuery)
	}
	if !got.Truncated || got.Scope != "all" || len(got.Capabilities) != 1 {
		t.Fatalf("unexpected catalog: %+v", got)
	}
	c := got.Capabilities[0]
	if c.MachineName != "web-01" || c.RemoteUser != "root" || c.Status != "held" || c.ExpiresAt == nil {
		t.Errorf("capability decoded wrong: %+v", c)
	}
}

func TestListCapabilitiesDefaultsOmitQueryAndEmptyList(t *testing.T) {
	var gotQuery string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotQuery = r.URL.RawQuery
		_, _ = w.Write([]byte(`{"user_id":"u1","scope":"related","capabilities":null}`))
	}))
	defer server.Close()

	client, err := NewClient(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	got, err := client.ListCapabilities(context.Background(), "", "")
	if err != nil {
		t.Fatalf("ListCapabilities: %v", err)
	}
	if gotQuery != "" {
		t.Errorf("query = %q, want none", gotQuery)
	}
	if got.Capabilities == nil {
		t.Error("a null list should decode as empty, not nil")
	}
}
