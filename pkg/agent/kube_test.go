package agent

import (
	"bufio"
	"context"
	"encoding/json"
	"encoding/pem"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	gossh "golang.org/x/crypto/ssh"

	"github.com/orion-belt-dev/orion-belt/pkg/kube"
)

// fakeAPIServer stores bindings by request path and records who proxied
// requests claimed to be.
type fakeAPIServer struct {
	*httptest.Server
	mu          sync.Mutex
	objects     map[string]json.RawMessage
	impersonate []string
}

func startFakeAPIServer(t *testing.T) (*fakeAPIServer, string, string) {
	t.Helper()
	f := &fakeAPIServer{objects: map[string]json.RawMessage{}}
	f.Server = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		if u := r.Header.Get("Impersonate-User"); u != "" {
			f.impersonate = append(f.impersonate, u)
			_, _ = w.Write([]byte(`{"kind":"PodList","items":[]}`))
			return
		}
		switch r.Method {
		case http.MethodGet:
			kind := "RoleBinding"
			if strings.HasSuffix(r.URL.Path, "clusterrolebindings") {
				kind = "ClusterRoleBinding"
			}
			var items []json.RawMessage
			for _, o := range f.objects {
				if strings.Contains(string(o), `"kind":"`+kind+`"`) {
					items = append(items, o)
				}
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"items": items})
		case http.MethodPost:
			body, _ := io.ReadAll(r.Body)
			var o struct {
				Metadata struct{ Name string } `json:"metadata"`
			}
			_ = json.Unmarshal(body, &o)
			f.objects[r.URL.Path+"/"+o.Metadata.Name] = body
			w.WriteHeader(http.StatusCreated)
		case http.MethodDelete:
			delete(f.objects, r.URL.Path)
		}
	}))
	t.Cleanup(f.Close)
	dir := t.TempDir()
	ca := filepath.Join(dir, "ca.crt")
	_ = os.WriteFile(ca, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: f.Certificate().Raw}), 0o600)
	token := filepath.Join(dir, "token")
	_ = os.WriteFile(token, []byte("sa"), 0o600)
	return f, ca, token
}

func (f *fakeAPIServer) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.objects)
}

// kubeConnected is connected() with the agent configured as in-cluster.
func kubeConnected(t *testing.T) (*Agent, *gossh.ServerConn, *gateway, *fakeAPIServer) {
	t.Helper()
	api, ca, token := startFakeAPIServer(t)
	g := startGateway(t)
	a, _ := agentFixture(t, g)
	a.config.Agent.Kubernetes.Enabled = true
	a.config.Agent.Kubernetes.APIServer = api.URL
	a.config.Agent.Kubernetes.CAFile = ca
	a.config.Agent.Kubernetes.TokenFile = token
	if err := a.connectToServer(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = a.Stop(context.Background()) })
	return a, g.waitConn(t), g, api
}

func kubeOp(t *testing.T, conn *gossh.ServerConn, req kube.Request) ([]kube.Binding, error) {
	t.Helper()
	data, _ := json.Marshal(req)
	ch, reqs, err := conn.OpenChannel(kube.AgentChannelType, data)
	if err != nil {
		return nil, err
	}
	go gossh.DiscardRequests(reqs)
	defer ch.Close()
	return kube.ReadResponse(ch)
}

func TestKube_AgentAnnouncesAndManagesBindings(t *testing.T) {
	_, conn, g, api := kubeConnected(t)

	deadline := time.Now().Add(5 * time.Second)
	for {
		g.mu.Lock()
		n := g.kubeAnnounce
		g.mu.Unlock()
		if n == 1 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("agent never announced Kubernetes")
		}
		time.Sleep(10 * time.Millisecond)
	}

	exp := time.Now().Add(time.Hour)
	b := kube.Desired([]kube.Grant{{PermissionID: "perm-1", Username: "alice", Scopes: []string{"payments/edit"}, ExpiresAt: &exp}})[0]
	if _, err := kubeOp(t, conn, kube.Request{Op: kube.OpApply, Binding: &b}); err != nil {
		t.Fatalf("apply: %v", err)
	}
	got, err := kubeOp(t, conn, kube.Request{Op: kube.OpList})
	if err != nil || len(got) != 1 || got[0].Subject != "orion:alice" {
		t.Fatalf("list = %+v, %v", got, err)
	}
	if _, err := kubeOp(t, conn, kube.Request{Op: kube.OpDelete, Binding: &b}); err != nil || api.count() != 0 {
		t.Fatalf("delete: %v (left %d)", err, api.count())
	}

	// Refusals come back as errors, not as a silent success.
	evil := kube.Binding{Name: "cluster-admin", ClusterRole: "cluster-admin", Subject: "orion:alice"}
	if _, err := kubeOp(t, conn, kube.Request{Op: kube.OpApply, Binding: &evil}); err == nil {
		t.Error("agent applied a binding that is not Orion Belt's")
	}
	if _, err := kubeOp(t, conn, kube.Request{Op: kube.OpApply}); err == nil {
		t.Error("apply without a binding succeeded")
	}
	if _, err := kubeOp(t, conn, kube.Request{Op: "exec"}); err == nil {
		t.Error("unknown op succeeded")
	}
	if _, err := kubeOp(t, conn, kube.Request{Op: kube.OpProxy}); err == nil {
		t.Error("proxy without a user accepted")
	}
	if _, _, err := conn.OpenChannel(kube.AgentChannelType, []byte("{")); err == nil {
		t.Error("malformed request accepted")
	}
}

func TestKube_AgentProxyImpersonates(t *testing.T) {
	_, conn, _, api := kubeConnected(t)
	data, _ := json.Marshal(kube.Request{Op: kube.OpProxy, User: "alice"})
	ch, reqs, err := conn.OpenChannel(kube.AgentChannelType, data)
	if err != nil {
		t.Fatal(err)
	}
	go gossh.DiscardRequests(reqs)
	defer ch.Close()

	req, _ := http.NewRequest(http.MethodGet, "http://kube/api/v1/namespaces/payments/pods", nil)
	req.Header.Set("Impersonate-User", "system:admin")
	if err := req.Write(ch); err != nil {
		t.Fatal(err)
	}
	resp, err := http.ReadResponse(bufio.NewReader(ch), req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	api.mu.Lock()
	defer api.mu.Unlock()
	if resp.StatusCode != http.StatusOK || len(api.impersonate) != 1 || api.impersonate[0] != "orion:alice" {
		t.Fatalf("status %d, impersonated %v", resp.StatusCode, api.impersonate)
	}
}

func TestKube_SweepDeletesExpiredBindings(t *testing.T) {
	a, conn, _, api := kubeConnected(t)
	past, later := time.Now().Add(-time.Minute), time.Now().Add(time.Hour)
	for _, g := range []kube.Grant{
		{PermissionID: "old-1", Username: "alice", Scopes: []string{"*/view"}, ExpiresAt: &past},
		{PermissionID: "new-1", Username: "bob", Scopes: []string{"ops/edit"}, ExpiresAt: &later},
		{PermissionID: "perm-1", Username: "carol", Scopes: []string{"ops/view"}},
	} {
		b := kube.Desired([]kube.Grant{g})[0]
		if _, err := kubeOp(t, conn, kube.Request{Op: kube.OpApply, Binding: &b}); err != nil {
			t.Fatal(err)
		}
	}
	// The agent removes the expired one itself, gateway or not.
	if n := a.sweepKube(context.Background()); n != 1 || api.count() != 2 {
		t.Fatalf("swept %d, %d left", n, api.count())
	}

	prev := kubeSweepInterval
	kubeSweepInterval = 10 * time.Millisecond
	t.Cleanup(func() { kubeSweepInterval = prev })
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { a.runKubeSweep(ctx); close(done) }()
	time.Sleep(30 * time.Millisecond)
	cancel()
	<-done
	if api.count() != 2 {
		t.Errorf("loop deleted live bindings: %d left", api.count())
	}
}

func TestKube_DisabledOrBrokenAgent(t *testing.T) {
	a, conn, _ := connected(t)
	_, err := kubeOp(t, conn, kube.Request{Op: kube.OpList})
	var openErr *gossh.OpenChannelError
	if !errors.As(err, &openErr) || openErr.Reason != gossh.Prohibited {
		t.Fatalf("non-cluster agent: %v", err)
	}
	if a.sweepKube(context.Background()) != 0 {
		t.Error("sweep ran without Kubernetes")
	}

	// Enabled but outside a cluster with no API server: it says so instead of
	// announcing itself.
	b, _ := agentFixture(t, nil)
	b.config.Agent.Kubernetes.Enabled = true
	t.Setenv("KUBERNETES_SERVICE_HOST", "")
	if c, err := b.kubeClient(); err == nil || c != nil {
		t.Errorf("kubeClient = %v, %v", c, err)
	}
	b.announceKubernetes(conn) // logs, does not send
}
