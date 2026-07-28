package sdk

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

func TestListMachinesUsesAPIV1Prefix(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/machines" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode([]Machine{{ID: "m1", Name: "app-01"}})
	}))
	defer server.Close()

	client, err := NewClient(server.URL)
	if err != nil {
		t.Fatalf("NewClient error: %v", err)
	}

	machines, err := client.ListMachines(context.Background())
	if err != nil {
		t.Fatalf("ListMachines error: %v", err)
	}
	if len(machines) != 1 || machines[0].ID != "m1" {
		t.Fatalf("unexpected machines: %+v", machines)
	}
}

func TestAuthHeadersPreferAPIKey(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("X-API-Key"); got != "k123" {
			t.Fatalf("expected X-API-Key header, got %q", got)
		}
		if got := r.Header.Get("X-Session-Token"); got != "" {
			t.Fatalf("expected no X-Session-Token when API key set, got %q", got)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer jwt123" {
			t.Fatalf("expected bearer auth header, got %q", got)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`[]`))
	}))
	defer server.Close()

	client, err := NewClient(server.URL,
		WithAPIKey("k123"),
		WithSessionToken("s123"),
		WithBearerToken("jwt123"),
	)
	if err != nil {
		t.Fatalf("NewClient error: %v", err)
	}

	if _, err := client.ListMachines(context.Background()); err != nil {
		t.Fatalf("ListMachines error: %v", err)
	}
}

func TestAPIErrorIncludesServerMessage(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"error":"denied"}`))
	}))
	defer server.Close()

	client, err := NewClient(server.URL)
	if err != nil {
		t.Fatalf("NewClient error: %v", err)
	}

	_, err = client.ListMachines(context.Background())
	if err == nil {
		t.Fatal("expected error")
	}

	var apiErr *APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("expected APIError, got %T (%v)", err, err)
	}
	if apiErr.StatusCode != http.StatusForbidden {
		t.Fatalf("unexpected status code: %d", apiErr.StatusCode)
	}
	if apiErr.Message != "denied" {
		t.Fatalf("unexpected message: %q", apiErr.Message)
	}
	if !strings.Contains(apiErr.Error(), "denied") {
		t.Fatalf("expected error text to include message, got %q", apiErr.Error())
	}
}

func TestGetCurrentUserParsesPayload(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/auth/me" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"u1","username":"admin","email":"a@example.com","role":"admin","mfa_enabled":true}`))
	}))
	defer server.Close()

	client, err := NewClient(server.URL, WithSessionToken("sess"))
	if err != nil {
		t.Fatalf("NewClient error: %v", err)
	}

	me, err := client.GetCurrentUser(context.Background())
	if err != nil {
		t.Fatalf("GetCurrentUser error: %v", err)
	}
	if me.ID != "u1" || me.Username != "admin" || me.Role != "admin" {
		t.Fatalf("unexpected user payload: %+v", me)
	}
}

func TestGetUsageDashboardBuildsQuery(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/dashboard/usage" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		q := r.URL.Query()
		if q.Get("window_hours") != "24" || q.Get("top") != "5" {
			t.Fatalf("unexpected query: %s", r.URL.RawQuery)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"window_hours":24,"from":"2026-07-23T00:00:00Z","to":"2026-07-24T00:00:00Z","generated_at":"2026-07-24T00:00:00Z","access_volume":{"sessions_total":1,"sessions_active":1,"requests_total":1,"requests_pending":0,"requests_approved":1,"requests_rejected":0},"approval_latency":{"sample_size":1,"average_seconds":30,"p50_seconds":30,"p95_seconds":30},"top_targets":[]}`))
	}))
	defer server.Close()

	client, err := NewClient(server.URL, WithAPIKey("k123"))
	if err != nil {
		t.Fatalf("NewClient error: %v", err)
	}

	usage, err := client.GetUsageDashboard(context.Background(), 24, 5)
	if err != nil {
		t.Fatalf("GetUsageDashboard error: %v", err)
	}
	if usage.WindowHours != 24 || usage.AccessVolume.SessionsTotal != 1 {
		t.Fatalf("unexpected usage payload: %+v", usage)
	}
}

func TestExportReportReturnsBytes(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/reports/ops/export" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		if got := r.URL.Query().Get("format"); got != "csv" {
			t.Fatalf("unexpected format query: %s", r.URL.RawQuery)
		}
		w.Header().Set("Content-Type", "text/csv")
		_, _ = w.Write([]byte("col1,col2\n1,2\n"))
	}))
	defer server.Close()

	client, err := NewClient(server.URL, WithAPIKey("k123"))
	if err != nil {
		t.Fatalf("NewClient error: %v", err)
	}

	b, err := client.ExportReport(context.Background(), "ops", "csv")
	if err != nil {
		t.Fatalf("ExportReport error: %v", err)
	}
	if string(b) != "col1,col2\n1,2\n" {
		t.Fatalf("unexpected report body: %q", string(b))
	}
}

func TestListSessionsEncodesStatusQuery(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/sessions" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		if got := r.URL.Query().Get("status"); got != "active" {
			t.Fatalf("unexpected status query: %s", r.URL.RawQuery)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`[]`))
	}))
	defer server.Close()

	client, err := NewClient(server.URL, WithAPIKey("k123"))
	if err != nil {
		t.Fatalf("NewClient error: %v", err)
	}

	_, err = client.ListSessions(context.Background(), "active")
	if err != nil {
		t.Fatalf("ListSessions error: %v", err)
	}
}

func TestListSessionsEscapesStatusQuery(t *testing.T) {
	status := "needs review"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/sessions" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		if got := r.URL.Query().Get("status"); got != status {
			t.Fatalf("unexpected status query: %s", r.URL.RawQuery)
		}
		if !strings.Contains(r.URL.RawQuery, "status="+url.QueryEscape(status)) {
			t.Fatalf("status not url-escaped as expected: %s", r.URL.RawQuery)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`[]`))
	}))
	defer server.Close()

	client, err := NewClient(server.URL, WithAPIKey("k123"))
	if err != nil {
		t.Fatalf("NewClient error: %v", err)
	}

	_, err = client.ListSessions(context.Background(), status)
	if err != nil {
		t.Fatalf("ListSessions error: %v", err)
	}
}

func TestUpdatePluginConfigEscapesPluginName(t *testing.T) {
	pluginName := "chat ops"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/admin/plugins/chat ops/config" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		if !strings.Contains(r.RequestURI, "chat%20ops") {
			t.Fatalf("expected escaped plugin name in request URI: %s", r.RequestURI)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"plugin":{"name":"chat ops","enabled":true}}`))
	}))
	defer server.Close()

	client, err := NewClient(server.URL, WithAPIKey("k123"))
	if err != nil {
		t.Fatalf("NewClient error: %v", err)
	}

	plugin, configureErr, err := client.UpdatePluginConfig(context.Background(), pluginName, true, map[string]interface{}{"channel": "ops"})
	if err != nil {
		t.Fatalf("UpdatePluginConfig error: %v", err)
	}
	if configureErr != "" {
		t.Fatalf("unexpected configure error: %q", configureErr)
	}
	if plugin.Name != pluginName || !plugin.Enabled {
		t.Fatalf("unexpected plugin payload: %+v", plugin)
	}
}

func TestDeleteMachineArchiveAddsQuery(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodDelete {
			t.Fatalf("unexpected method: %s", r.Method)
		}
		if r.URL.Path != "/api/v1/admin/machines/m1" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		if r.URL.Query().Get("archive") != "true" {
			t.Fatalf("unexpected query: %s", r.URL.RawQuery)
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	client, err := NewClient(server.URL, WithAPIKey("k123"))
	if err != nil {
		t.Fatalf("NewClient error: %v", err)
	}

	if err := client.DeleteMachine(context.Background(), "m1", true); err != nil {
		t.Fatalf("DeleteMachine error: %v", err)
	}
}

func TestIssueChallengeUsesPublicEndpoint(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/public/auth/challenge" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		if r.Method != http.MethodPost {
			t.Fatalf("unexpected method: %s", r.Method)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"challenge":"abc123"}`))
	}))
	defer server.Close()

	client, err := NewClient(server.URL)
	if err != nil {
		t.Fatalf("NewClient error: %v", err)
	}

	challenge, err := client.IssueChallenge(context.Background(), "admin")
	if err != nil {
		t.Fatalf("IssueChallenge error: %v", err)
	}
	if challenge != "abc123" {
		t.Fatalf("unexpected challenge: %q", challenge)
	}
}

func TestListFilesBuildsQuery(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/files/list" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		q := r.URL.Query()
		if q.Get("machine") != "m1" || q.Get("path") != "/etc" || q.Get("user") != "root" {
			t.Fatalf("unexpected query: %s", r.URL.RawQuery)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"path":"/etc","entries":[]}`))
	}))
	defer server.Close()

	client, err := NewClient(server.URL, WithAPIKey("k123"))
	if err != nil {
		t.Fatalf("NewClient error: %v", err)
	}

	resp, err := client.ListFiles(context.Background(), "m1", "/etc", "root")
	if err != nil {
		t.Fatalf("ListFiles error: %v", err)
	}
	if resp.Path != "/etc" {
		t.Fatalf("unexpected file list response: %+v", resp)
	}
}

func TestUploadFileUsesMultipart(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/files/upload" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		if r.Method != http.MethodPost {
			t.Fatalf("unexpected method: %s", r.Method)
		}
		if !strings.Contains(r.Header.Get("Content-Type"), "multipart/form-data") {
			t.Fatalf("expected multipart content type, got %q", r.Header.Get("Content-Type"))
		}

		err := r.ParseMultipartForm(1024 * 1024)
		if err != nil {
			t.Fatalf("ParseMultipartForm error: %v", err)
		}
		if r.FormValue("machine") != "m1" || r.FormValue("path") != "/tmp/test.txt" || r.FormValue("user") != "root" {
			t.Fatalf("unexpected form values: machine=%q path=%q user=%q", r.FormValue("machine"), r.FormValue("path"), r.FormValue("user"))
		}

		f, _, err := r.FormFile("file")
		if err != nil {
			t.Fatalf("FormFile error: %v", err)
		}
		defer f.Close()
		b, err := io.ReadAll(f)
		if err != nil {
			t.Fatalf("ReadAll form file error: %v", err)
		}
		if !bytes.Equal(b, []byte("hello")) {
			t.Fatalf("unexpected file body: %q", string(b))
		}

		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"message":"uploaded","path":"/tmp/test.txt","size":5}`))
	}))
	defer server.Close()

	client, err := NewClient(server.URL, WithSessionToken("s123"))
	if err != nil {
		t.Fatalf("NewClient error: %v", err)
	}

	resp, err := client.UploadFile(context.Background(), "m1", "/tmp/test.txt", "root", "test.txt", []byte("hello"))
	if err != nil {
		t.Fatalf("UploadFile error: %v", err)
	}
	if resp.Message != "uploaded" || resp.Size != 5 {
		t.Fatalf("unexpected upload response: %+v", resp)
	}
}
