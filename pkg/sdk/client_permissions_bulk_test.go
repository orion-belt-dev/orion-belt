package sdk

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestBulkAndEffectivePermissions(t *testing.T) {
	var seen []string
	var bodies []map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = append(seen, r.Method+" "+r.URL.RequestURI())
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		bodies = append(bodies, body)
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/v1/admin/permissions/bulk":
			_, _ = w.Write([]byte(`{"granted":3,"failed":[{"user_id":"ghost","machine_id":"m1","error":"x"}]}`))
		case "/api/v1/admin/permissions/bulk-revoke":
			_, _ = w.Write([]byte(`{"revoked":2,"failed":[]}`))
		case "/api/v1/admin/permissions/effective":
			if r.URL.Query().Get("machine_id") == "empty" {
				_, _ = w.Write([]byte(`null`))
				return
			}
			_, _ = w.Write([]byte(`[{"user_id":"u1","username":"alice","machine_id":"m1","machine_name":"web-01","access_type":"ssh","remote_users":["deploy"],"source":"permission:p1","expires_at":"2026-11-01T00:00:00Z"}]`))
		}
	}))
	defer server.Close()
	client, err := NewClient(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()

	d := 600
	res, err := client.BulkGrantPermissions(ctx, BulkGrantRequest{UserIDs: []string{"u1"}, MachineIDs: []string{"m1"}, AccessType: "ssh", RemoteUsers: []string{"deploy"}, DurationSec: &d})
	if err != nil || res.Granted != 3 || len(res.Failed) != 1 {
		t.Fatalf("BulkGrant = %+v, %v", res, err)
	}
	if bodies[0]["access_type"] != "ssh" || bodies[0]["duration_seconds"] != float64(600) {
		t.Errorf("bulk grant body = %v", bodies[0])
	}
	if res, err := client.BulkRevokePermissions(ctx, []string{"p1", "p2"}); err != nil || res.Revoked != 2 {
		t.Fatalf("BulkRevoke = %+v, %v", res, err)
	}
	if ids, _ := bodies[1]["ids"].([]any); len(ids) != 2 {
		t.Errorf("bulk revoke body = %v", bodies[1])
	}
	rows, err := client.EffectivePermissions(ctx, "u1", "")
	if err != nil || len(rows) != 1 || rows[0].Source != "permission:p1" || rows[0].ExpiresAt == nil {
		t.Fatalf("Effective = %+v, %v", rows, err)
	}
	if rows, err := client.EffectivePermissions(ctx, "", "empty"); err != nil || rows == nil || len(rows) != 0 {
		t.Errorf("empty effective = %#v, %v", rows, err)
	}
	if seen[2] != "GET /api/v1/admin/permissions/effective?user_id=u1" || seen[3] != "GET /api/v1/admin/permissions/effective?machine_id=empty" {
		t.Errorf("requests = %v", seen)
	}
}
