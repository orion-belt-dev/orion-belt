package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"

	"github.com/orion-belt-dev/orion-belt/pkg/common"
	"github.com/orion-belt-dev/orion-belt/pkg/kube"
)

// stubCluster is the in-cluster agent's view of a cluster: the Orion Belt
// bindings that exist, and who the proxy acted as.
type stubCluster struct {
	mu        sync.Mutex
	bindings  map[string]kube.Binding // namespace/name
	proxied   []string
	failApply bool
}

func newStubCluster() *stubCluster { return &stubCluster{bindings: map[string]kube.Binding{}} }

func (c *stubCluster) snapshot() []kube.Binding {
	c.mu.Lock()
	defer c.mu.Unlock()
	var out []kube.Binding
	for _, b := range c.bindings {
		out = append(out, b)
	}
	return out
}

func (c *stubCluster) put(b kube.Binding) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.bindings[b.Namespace+"/"+b.Name] = b
}

// kube serves a gateway Kubernetes operation as the real agent does, against
// the stub cluster. The proxy echoes, standing in for the API server.
func (a *stubAgent) kube(nc ssh.NewChannel) {
	if a.cluster == nil {
		_ = nc.Reject(ssh.Prohibited, "kubernetes is not enabled on this agent")
		return
	}
	var req kube.Request
	_ = json.Unmarshal(nc.ExtraData(), &req)
	ch, reqs, err := nc.Accept()
	if err != nil {
		return
	}
	go ssh.DiscardRequests(reqs)
	go func() {
		defer ch.Close()
		c := a.cluster
		switch req.Op {
		case kube.OpProxy:
			c.mu.Lock()
			c.proxied = append(c.proxied, req.User)
			c.mu.Unlock()
			_, _ = io.Copy(ch, ch)
		case kube.OpList:
			_ = kube.WriteResponse(ch, c.snapshot(), nil)
		case kube.OpApply:
			c.mu.Lock()
			fail := c.failApply
			c.mu.Unlock()
			if fail {
				_ = kube.WriteResponse(ch, nil, errors.New("forbidden"))
				break
			}
			c.put(*req.Binding)
			_ = kube.WriteResponse(ch, nil, nil)
		case kube.OpDelete:
			c.mu.Lock()
			delete(c.bindings, req.Binding.Namespace+"/"+req.Binding.Name)
			c.mu.Unlock()
			_ = kube.WriteResponse(ch, nil, nil)
		}
		_ = ch.CloseWrite()
	}()
}

// connectKubeAgent connects the machine's agent as an in-cluster agent and
// waits until the gateway has registered it as one.
func (h *harness) connectKubeAgent(t *testing.T, cluster *stubCluster) *stubAgent {
	t.Helper()
	a := h.connectAgent(t)
	a.cluster = cluster
	if _, _, err := a.conn.SendRequest(kube.CapabilityRequest, true, nil); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := h.srv.kubeAgent(h.machine.ID); err == nil {
			return a
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("gateway never marked the agent as in-cluster")
	return nil
}

func (h *harness) grantK8s(t *testing.T, scopes []string, expires *time.Time) *common.Permission {
	t.Helper()
	p := common.NewPermission(h.alice.ID, h.machine.ID, common.AccessTypeK8s, scopes, h.alice.ID, expires)
	if err := h.srv.store.CreatePermission(context.Background(), p); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestKube_ReconcileIssuesAndRemovesBindings(t *testing.T) {
	h := startHarness(t, nil)
	cluster := newStubCluster()
	h.connectKubeAgent(t, cluster)
	ctx := context.Background()

	later := time.Now().Add(time.Hour)
	perm := h.grantK8s(t, []string{"payments/edit", "*/view"}, &later)

	// A dry run reports and changes nothing.
	plan, err := h.srv.ReconcileKube(ctx, h.machine.ID, true)
	if err != nil || len(plan.Create) != 2 {
		t.Fatalf("dry run = %+v, %v", plan, err)
	}
	if got := cluster.snapshot(); len(got) != 0 {
		t.Fatalf("dry run created %v", got)
	}

	if _, err := h.srv.ReconcileKube(ctx, h.machine.ID, false); err != nil {
		t.Fatal(err)
	}
	got := cluster.snapshot()
	if len(got) != 2 {
		t.Fatalf("bindings = %+v", got)
	}
	kinds := map[string]bool{}
	for _, b := range got {
		kinds[b.Kind()] = true
		if b.Subject != "orion:alice" || b.Permission != perm.ID || b.ExpiresAt == nil {
			t.Errorf("binding %+v", b)
		}
	}
	if !kinds["RoleBinding"] || !kinds["ClusterRoleBinding"] {
		t.Errorf("want one of each kind, got %v", kinds)
	}
	if plan, _ := h.srv.ReconcileKube(ctx, h.machine.ID, true); !plan.Empty() {
		t.Errorf("not in sync after apply: %+v", plan)
	}

	// Drift: a binding left by a bug, one whose grant expired, and the grant
	// revoked. All three go, each audited with its reason.
	past := time.Now().Add(-time.Minute)
	cluster.put(kube.Binding{Name: "orion-deadbeef-00000000", Namespace: "x", ClusterRole: "view", Subject: "orion:eve", User: "eve"})
	cluster.put(kube.Binding{Name: "orion-cafecafe-00000000", ClusterRole: "view", Subject: "orion:bob", User: "bob", Permission: "gone", ExpiresAt: &past})
	if err := h.srv.store.DeletePermission(ctx, perm.ID); err != nil {
		t.Fatal(err)
	}
	plan, err = h.srv.ReconcileKube(ctx, h.machine.ID, false)
	if err != nil || len(plan.Delete) != 4 {
		t.Fatalf("cleanup plan = %+v, %v", plan, err)
	}
	if got := cluster.snapshot(); len(got) != 0 {
		t.Fatalf("left %+v", got)
	}

	reasons := map[string]int{}
	logs, _ := h.srv.store.ListAuditLogs(ctx, 100, 0, map[string]interface{}{"action_prefix": "k8s.binding."})
	for _, l := range logs {
		reasons[fmt.Sprintf("%s:%v", l.Action, l.Metadata["reason"])]++
		if l.Action == "k8s.binding.create" && l.UserID != h.alice.ID {
			t.Errorf("create audited for %q, want alice", l.UserID)
		}
	}
	for key, n := range map[string]int{
		"k8s.binding.create:granted":  2,
		"k8s.binding.delete:revoked":  2,
		"k8s.binding.delete:orphaned": 1,
		"k8s.binding.delete:expired":  1,
	} {
		if reasons[key] != n {
			t.Errorf("audit %s = %d, want %d (all: %v)", key, reasons[key], n, reasons)
		}
	}
}

func TestKube_ReconcileFailuresAreReportedAndAudited(t *testing.T) {
	h := startHarness(t, nil)
	cluster := newStubCluster()
	cluster.failApply = true
	h.connectKubeAgent(t, cluster)
	h.grantK8s(t, []string{"payments/edit"}, nil)

	if _, err := h.srv.ReconcileKube(context.Background(), h.machine.ID, false); err == nil || !strings.Contains(err.Error(), "forbidden") {
		t.Fatalf("reconcile = %v, want the agent's error", err)
	}
	logs, _ := h.srv.store.ListAuditLogs(context.Background(), 10, 0, map[string]interface{}{"action_prefix": "k8s.binding.create_failed"})
	if len(logs) != 1 || logs[0].Metadata["error"] == nil {
		t.Errorf("failure audit = %+v", logs)
	}
}

func TestKube_AgentReconnectReconciles(t *testing.T) {
	h := startHarness(t, nil)
	cluster := newStubCluster()
	a := h.connectKubeAgent(t, cluster)
	h.grantK8s(t, []string{"*/view"}, nil)
	if _, err := h.srv.ReconcileKube(context.Background(), h.machine.ID, false); err != nil {
		t.Fatal(err)
	}
	if len(cluster.snapshot()) != 1 {
		t.Fatal("binding not issued")
	}

	// The agent drops, as it does when the gateway restarts; the grant is
	// revoked meanwhile. Reconnecting alone removes the binding.
	a.conn.Close()
	eventually(t, "agent to unregister", func() bool {
		_, err := h.srv.kubeAgent(h.machine.ID)
		return err != nil
	})
	perms, _ := h.srv.store.ListMachinePermissions(context.Background(), h.machine.ID)
	for _, p := range perms {
		_ = h.srv.store.DeletePermission(context.Background(), p.ID)
	}
	h.connectKubeAgent(t, cluster)
	eventually(t, "reconnect reconcile to delete the binding", func() bool { return len(cluster.snapshot()) == 0 })
}

func TestKube_LoopReconcilesConnectedClusters(t *testing.T) {
	prev := kubeReconcileInterval
	kubeReconcileInterval = 50 * time.Millisecond
	t.Cleanup(func() { kubeReconcileInterval = prev })

	h := startHarness(t, nil)
	cluster := newStubCluster()
	h.connectKubeAgent(t, cluster)
	h.grantK8s(t, []string{"ops/admin"}, nil)
	eventually(t, "loop to issue the binding", func() bool { return len(cluster.snapshot()) == 1 })
}

func TestKube_NotAnInClusterAgent(t *testing.T) {
	h := startHarness(t, nil)
	if _, err := h.srv.ReconcileKube(context.Background(), h.machine.ID, true); !errors.Is(err, kube.ErrNoAgent) {
		t.Fatalf("offline = %v", err)
	}
	h.connectAgent(t) // a plain host agent
	if _, err := h.srv.ReconcileKube(context.Background(), h.machine.ID, true); !errors.Is(err, kube.ErrNoAgent) {
		t.Fatalf("host agent = %v", err)
	}
	h.srv.KubeGrantChanged(h.machine.ID) // must not panic or log an error path
}

// kubectlEcho opens the osh kubectl channel to cluster as alice and returns
// what the relay echoed for msg.
func (h *harness) kubectlEcho(t *testing.T, cluster, msg string) (string, error) {
	t.Helper()
	c, err := h.dial(t, "alice", h.aliceKey)
	if err != nil {
		t.Fatal(err)
	}
	ch, reqs, err := c.OpenChannel(kube.ClientChannelType, ssh.Marshal(&kube.ClientChannelData{Cluster: cluster}))
	if err != nil {
		return "", err
	}
	go ssh.DiscardRequests(reqs)
	defer ch.Close()
	_, _ = io.WriteString(ch, msg)
	_ = ch.CloseWrite()
	got, err := io.ReadAll(ch)
	return string(got), err
}

func TestKube_KubectlRelay(t *testing.T) {
	h := startHarness(t, nil)

	// Cluster known but its agent is not in-cluster yet.
	h.grantK8s(t, []string{"payments/view"}, nil)
	if _, err := h.kubectlEcho(t, "web-01", "x"); err == nil || !strings.Contains(err.Error(), "not available") {
		t.Fatalf("no agent: %v", err)
	}
	cluster := newStubCluster()
	h.connectKubeAgent(t, cluster)

	got, err := h.kubectlEcho(t, "web-01", "GET /api HTTP/1.1")
	if err != nil || got != "GET /api HTTP/1.1" {
		t.Fatalf("relay = %q, %v", got, err)
	}
	cluster.mu.Lock()
	proxied := cluster.proxied
	cluster.mu.Unlock()
	if len(proxied) != 1 || proxied[0] != "alice" {
		t.Errorf("agent proxied as %v, want alice", proxied)
	}
	if _, err := h.kubectlEcho(t, "ghost", "x"); err == nil || !strings.Contains(err.Error(), "cluster not found") {
		t.Errorf("unknown cluster: %v", err)
	}

	// An ssh grant, or no grant at all, does not open the API.
	perms, _ := h.srv.store.ListMachinePermissions(context.Background(), h.machine.ID)
	for _, p := range perms {
		_ = h.srv.store.DeletePermission(context.Background(), p.ID)
	}
	h.grantType(t, common.AccessTypeBoth, []string{"root"})
	if _, err := h.kubectlEcho(t, "web-01", "x"); err == nil || !strings.Contains(err.Error(), "no k8s access") {
		t.Fatalf("with only an ssh grant: %v", err)
	}

	eventually(t, "k8s.access audit", func() bool {
		actions := strings.Join(h.auditActions(t, "k8s.access."), ",")
		return strings.Contains(actions, "k8s.access.close") && strings.Contains(actions, "k8s.access.denied")
	})

	// Malformed channel data is refused.
	c, _ := h.dial(t, "alice", h.aliceKey)
	if _, _, err := c.OpenChannel(kube.ClientChannelType, []byte{1}); err == nil {
		t.Error("malformed kube channel accepted")
	}
}
