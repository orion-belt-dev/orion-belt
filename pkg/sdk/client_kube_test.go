package sdk

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestKubeReconcileMethodsAndDecode(t *testing.T) {
	var methods []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/admin/machines/m1/kubernetes/reconcile" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		methods = append(methods, r.Method)
		if r.Method == http.MethodPost {
			w.WriteHeader(http.StatusConflict)
			_, _ = w.Write([]byte(`{"error":"no connected in-cluster Kubernetes agent"}`))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"machine_id":"m1","dry_run":true,"in_sync":false,"plan":{
			"create":[],"update":[],
			"delete":[{"name":"orion-a","namespace":"payments","cluster_role":"edit","subject":"orion:alice","user":"alice","permission_id":"p1","expires_at":"2026-10-24T10:00:00Z"}]}}`))
	}))
	defer server.Close()

	client, err := NewClient(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	got, err := client.KubeReconcile(context.Background(), "m1", true)
	if err != nil {
		t.Fatalf("dry run: %v", err)
	}
	if !got.DryRun || got.InSync || len(got.Plan.Delete) != 1 || got.Plan.Delete[0].ExpiresAt == nil || got.Plan.Delete[0].ClusterRole != "edit" {
		t.Errorf("decoded %+v", got)
	}
	if _, err := client.KubeReconcile(context.Background(), "m1", false); err == nil {
		t.Error("409 was not an error")
	}
	if len(methods) != 2 || methods[0] != http.MethodGet || methods[1] != http.MethodPost {
		t.Errorf("methods = %v, want GET then POST", methods)
	}
}
