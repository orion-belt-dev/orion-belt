// Package kubernetes_test runs the in-cluster agent's Kubernetes side against
// a real API server: bindings issued with the agent's own RBAC (from
// deploy/kubernetes), and requests through the proxy authorized by them.
//
// Opt in with ORION_KIND_E2E=<dir>, a directory holding:
//
//	server   the API server URL
//	ca.crt   its CA
//	token    a token for the orion-belt-agent service account
//
// and a namespace "payments". See docs/KUBERNETES.md for the kind setup.
package kubernetes_test

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/orion-belt-dev/orion-belt/pkg/kube"
)

func client(t *testing.T) *kube.Client {
	t.Helper()
	dir := os.Getenv("ORION_KIND_E2E")
	if dir == "" {
		t.Skip("set ORION_KIND_E2E to a directory with server, ca.crt and token to run")
	}
	server, err := os.ReadFile(filepath.Join(dir, "server"))
	if err != nil {
		t.Fatal(err)
	}
	c, err := kube.NewClient(kube.Config{
		APIServer: strings.TrimSpace(string(server)),
		CAFile:    filepath.Join(dir, "ca.crt"),
		TokenFile: filepath.Join(dir, "token"),
	})
	if err != nil {
		t.Fatal(err)
	}
	return c
}

// get calls path through the proxy as user, with a forged admin identity the
// proxy must drop, and returns the status.
func get(t *testing.T, proxy *httptest.Server, path string) int {
	t.Helper()
	req, _ := http.NewRequest(http.MethodGet, proxy.URL+path, nil)
	req.Header.Set("Impersonate-User", "system:admin")
	req.Header.Set("Impersonate-Group", "system:masters")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	resp.Body.Close()
	return resp.StatusCode
}

func TestKind_BindingsGrantExactlyTheirScope(t *testing.T) {
	c := client(t)
	ctx := context.Background()
	user := "e2e-" + time.Now().Format("150405")
	proxy := httptest.NewServer(kube.NewProxy(c, user, func(err error) { t.Log(err) }))
	defer proxy.Close()

	// Nothing issued: the forged identity gets the user nowhere.
	if code := get(t, proxy, "/api/v1/namespaces/payments/pods"); code != http.StatusForbidden {
		t.Fatalf("before any grant: %d, want 403", code)
	}

	later := time.Now().Add(time.Hour)
	bindings := kube.Desired([]kube.Grant{{PermissionID: "e2e-" + user, Username: user, Scopes: []string{"payments/view"}, ExpiresAt: &later}})
	for _, b := range bindings {
		if err := c.Apply(ctx, b); err != nil {
			t.Fatalf("apply with the agent's RBAC: %v", err)
		}
	}
	t.Cleanup(func() {
		for _, b := range bindings {
			_ = c.Delete(context.Background(), b)
		}
	})

	// RBAC propagates within a moment.
	deadline := time.Now().Add(10 * time.Second)
	for get(t, proxy, "/api/v1/namespaces/payments/pods") != http.StatusOK {
		if time.Now().After(deadline) {
			t.Fatal("payments/view binding never took effect")
		}
		time.Sleep(200 * time.Millisecond)
	}
	if code := get(t, proxy, "/api/v1/namespaces/default/pods"); code != http.StatusForbidden {
		t.Errorf("namespace outside the scope: %d, want 403", code)
	}
	if code := get(t, proxy, "/api/v1/namespaces/kube-system/secrets"); code != http.StatusForbidden {
		t.Errorf("secrets elsewhere: %d, want 403", code)
	}

	listed, err := c.List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if plan := kube.Diff(bindings, listed, time.Now()); len(plan.Create)+len(plan.Update) != 0 {
		t.Errorf("listed bindings differ from issued: %+v", plan)
	}

	for _, b := range bindings {
		if err := c.Delete(ctx, b); err != nil {
			t.Fatal(err)
		}
	}
	deadline = time.Now().Add(10 * time.Second)
	for get(t, proxy, "/api/v1/namespaces/payments/pods") != http.StatusForbidden {
		if time.Now().After(deadline) {
			t.Fatal("access outlived its binding")
		}
		time.Sleep(200 * time.Millisecond)
	}
}

func TestKind_ClusterWideScope(t *testing.T) {
	c := client(t)
	ctx := context.Background()
	user := "e2e-cw-" + time.Now().Format("150405")
	proxy := httptest.NewServer(kube.NewProxy(c, user, nil))
	defer proxy.Close()

	bindings := kube.Desired([]kube.Grant{{PermissionID: "e2e-" + user, Username: user, Scopes: []string{"*/view"}}})
	if err := c.Apply(ctx, bindings[0]); err != nil {
		t.Fatalf("apply ClusterRoleBinding: %v", err)
	}
	t.Cleanup(func() { _ = c.Delete(context.Background(), bindings[0]) })
	deadline := time.Now().Add(10 * time.Second)
	for get(t, proxy, "/api/v1/namespaces/default/pods") != http.StatusOK {
		if time.Now().After(deadline) {
			t.Fatal("*/view never took effect")
		}
		time.Sleep(200 * time.Millisecond)
	}
	// view never reads secrets, even cluster-wide.
	if code := get(t, proxy, "/api/v1/namespaces/default/secrets"); code != http.StatusForbidden {
		t.Errorf("secrets with */view: %d, want 403", code)
	}
}
