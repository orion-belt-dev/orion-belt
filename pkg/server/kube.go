package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"sync"
	"time"

	"github.com/orion-belt-dev/orion-belt/pkg/common"
	"github.com/orion-belt-dev/orion-belt/pkg/kube"
	"github.com/orion-belt-dev/orion-belt/pkg/tracing"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
	"golang.org/x/crypto/ssh"
)

// kubeReconcileInterval is how often the gateway reconciles every connected
// cluster. Grants and agent connections also trigger a reconcile at once;
// the loop is what catches expiry and anything a restart interrupted.
var kubeReconcileInterval = 30 * time.Second

// kubeLocks serializes reconciles per cluster, so the loop and a grant
// arriving together do not both create the same binding.
type kubeLocks struct {
	mu    sync.Mutex
	locks map[string]*sync.Mutex
}

func (k *kubeLocks) lock(machineID string) func() {
	k.mu.Lock()
	if k.locks == nil {
		k.locks = map[string]*sync.Mutex{}
	}
	l, ok := k.locks[machineID]
	if !ok {
		l = &sync.Mutex{}
		k.locks[machineID] = l
	}
	k.mu.Unlock()
	l.Lock()
	return l.Unlock
}

// kubeAgent returns the connection of machineID's agent when it announced
// itself as an in-cluster agent.
func (s *Server) kubeAgent(machineID string) (*AgentConnection, error) {
	s.agentsMu.RLock()
	defer s.agentsMu.RUnlock()
	conn, ok := s.agents[machineID]
	if !ok || !conn.Kubernetes {
		return nil, kube.ErrNoAgent
	}
	return conn, nil
}

// kubeOp runs one list, apply or delete operation on a cluster's agent.
func (s *Server) kubeOp(conn *AgentConnection, req kube.Request) ([]kube.Binding, error) {
	data, err := json.Marshal(req)
	if err != nil {
		return nil, err
	}
	ch, reqs, err := conn.SSHConn.Conn.OpenChannel(kube.AgentChannelType, data)
	if err != nil {
		return nil, fmt.Errorf("open kubernetes channel: %w", err)
	}
	defer ch.Close()
	go ssh.DiscardRequests(reqs)

	type result struct {
		bindings []kube.Binding
		err      error
	}
	done := make(chan result, 1)
	go func() {
		b, err := kube.ReadResponse(ch)
		done <- result{b, err}
	}()
	select {
	case r := <-done:
		return r.bindings, r.err
	case <-time.After(agentCommandTimeout + time.Minute):
		return nil, fmt.Errorf("kubernetes %s timed out", req.Op)
	}
}

// desiredKubeBindings is what the active k8s grants on a cluster call for.
func (s *Server) desiredKubeBindings(ctx context.Context, machineID string) ([]kube.Binding, error) {
	perms, err := s.store.ListMachinePermissions(ctx, machineID)
	if err != nil {
		return nil, fmt.Errorf("list grants: %w", err)
	}
	var grants []kube.Grant
	for _, p := range perms {
		if p.AccessType != common.AccessTypeK8s {
			continue
		}
		user, err := s.store.GetUser(ctx, p.UserID)
		if err != nil {
			// A grant whose user is gone binds nobody; its binding, if any,
			// is deleted as no longer desired.
			continue
		}
		grants = append(grants, kube.Grant{PermissionID: p.ID, Username: user.Username, Scopes: p.RemoteUsers, ExpiresAt: p.ExpiresAt})
	}
	return kube.Desired(grants), nil
}

// ReconcileKube brings a cluster's Orion Belt bindings in line with its
// active k8s grants, or with dryRun only reports what that would change. The
// returned plan lists what was (or would be) created, updated and deleted;
// with dryRun, Delete is how orphaned and expired bindings are detected.
func (s *Server) ReconcileKube(ctx context.Context, machineID string, dryRun bool) (kube.Plan, error) {
	conn, err := s.kubeAgent(machineID)
	if err != nil {
		return kube.Plan{}, err
	}
	defer s.kubeLocks.lock(machineID)()

	desired, err := s.desiredKubeBindings(ctx, machineID)
	if err != nil {
		return kube.Plan{}, err
	}
	actual, err := s.kubeOp(conn, kube.Request{Op: kube.OpList})
	if err != nil {
		return kube.Plan{}, err
	}
	now := time.Now()
	plan := kube.Diff(desired, actual, now)
	if dryRun || plan.Empty() {
		return plan, nil
	}

	var failed []error
	apply := func(op, action string, b kube.Binding, reason string) {
		if _, err := s.kubeOp(conn, kube.Request{Op: op, Binding: &b}); err != nil {
			failed = append(failed, fmt.Errorf("%s %s: %w", op, b.Name, err))
			s.auditKubeBinding(ctx, machineID, action+"_failed", b, reason, err)
			return
		}
		s.auditKubeBinding(ctx, machineID, action, b, reason, nil)
	}
	for _, b := range plan.Create {
		apply(kube.OpApply, "k8s.binding.create", b, "granted")
	}
	for _, b := range plan.Update {
		apply(kube.OpApply, "k8s.binding.update", b, "grant changed")
	}
	for _, b := range plan.Delete {
		apply(kube.OpDelete, "k8s.binding.delete", b, deleteReason(b, now))
	}
	if len(failed) > 0 {
		return plan, errors.Join(failed...)
	}
	return plan, nil
}

// deleteReason says why a binding no grant calls for is removed.
func deleteReason(b kube.Binding, now time.Time) string {
	switch {
	case b.Permission == "":
		return "orphaned"
	case b.Expired(now):
		return "expired"
	default:
		return "revoked"
	}
}

// auditKubeBinding records a binding change. The entry is attributed to the
// user the binding is for, like the access request that led to it, so a
// user's Kubernetes access reads as one history.
func (s *Server) auditKubeBinding(ctx context.Context, machineID, action string, b kube.Binding, reason string, failure error) {
	userID := ""
	if u, err := s.store.GetUserByUsername(ctx, b.User); err == nil {
		userID = u.ID
	}
	meta := map[string]interface{}{
		"machine_id":    machineID,
		"kind":          b.Kind(),
		"name":          b.Name,
		"namespace":     b.Namespace,
		"scope":         b.Scope().String(),
		"cluster_role":  b.ClusterRole,
		"subject":       b.Subject,
		"user":          b.User,
		"permission_id": b.Permission,
		"reason":        reason,
	}
	if b.ExpiresAt != nil {
		meta["expires_at"] = b.ExpiresAt.UTC().Format(time.RFC3339)
	}
	if failure != nil {
		meta["error"] = failure.Error()
	}
	if err := s.store.CreateAuditLog(ctx, common.NewAuditLog(userID, action, "machine:"+machineID, "", meta)); err != nil {
		s.logger.Warn("Failed to audit %s for %s: %v", action, b.Name, err)
	}
	if failure != nil {
		s.logger.Warn("Kubernetes %s %s on %s failed: %v", action, b.Name, machineID, failure)
	} else {
		s.logger.Info("Kubernetes %s %s/%s for %s (%s)", action, b.Namespace, b.Name, b.User, reason)
	}
}

// reconcileKubeAsync reconciles a cluster in the background, logging the
// outcome. Used where a change should reach the cluster now, not at the next
// loop tick.
func (s *Server) reconcileKubeAsync(machineID string) {
	go func() {
		if _, err := s.ReconcileKube(context.Background(), machineID, false); err != nil && !errors.Is(err, kube.ErrNoAgent) {
			s.logger.Warn("Kubernetes reconcile of %s: %v", machineID, err)
		}
	}()
}

// KubeGrantChanged is called by the API after a k8s grant on machineID is
// created, changed or revoked.
func (s *Server) KubeGrantChanged(machineID string) { s.reconcileKubeAsync(machineID) }

// runKubeReconcileLoop reconciles every connected cluster each interval.
func (s *Server) runKubeReconcileLoop() {
	ticker := time.NewTicker(s.kubeInterval)
	defer ticker.Stop()
	for {
		select {
		case <-s.shutdown:
			return
		case <-ticker.C:
			s.agentsMu.RLock()
			var clusters []string
			for id, conn := range s.agents {
				if conn.Kubernetes {
					clusters = append(clusters, id)
				}
			}
			s.agentsMu.RUnlock()
			for _, id := range clusters {
				if _, err := s.ReconcileKube(context.Background(), id, false); err != nil && !errors.Is(err, kube.ErrNoAgent) {
					s.logger.Warn("Kubernetes reconcile of %s: %v", id, err)
				}
			}
		}
	}
}

// markKubernetes records that machineID's agent runs in a cluster and
// reconciles it, which is what makes a gateway restart lose nothing: the
// agent reconnects, and bindings that expired meanwhile are deleted.
func (s *Server) markKubernetes(machineID string) {
	s.agentsMu.Lock()
	if conn, ok := s.agents[machineID]; ok {
		conn.Kubernetes = true
	}
	s.agentsMu.Unlock()
	s.reconcileKubeAsync(machineID)
}

// handleClientKube relays one kubectl connection to a cluster's API server
// through its in-cluster agent, which acts as the user. Kubernetes RBAC then
// enforces exactly the bindings the user's grants issued.
func (s *Server) handleClientKube(newChannel ssh.NewChannel, cc *clientConn) {
	userID, username := cc.userID, cc.username
	var req kube.ClientChannelData
	if err := ssh.Unmarshal(newChannel.ExtraData(), &req); err != nil {
		newChannel.Reject(ssh.ConnectionFailed, "malformed kubernetes request")
		return
	}
	if !cc.acquire() {
		newChannel.Reject(ssh.ResourceShortage, fmt.Sprintf("too many open tunnels on this connection (limit %d)", cc.limit))
		return
	}
	defer cc.release()

	ctx, span := tracing.Start(context.Background(), "gateway.kube",
		trace.WithSpanKind(trace.SpanKindServer))
	defer span.End()
	tracing.SetAttributes(span,
		attribute.String("orion.user", username),
		attribute.String("orion.target.machine", req.Cluster),
	)

	machine, err := s.store.GetMachineByName(ctx, req.Cluster)
	if err != nil {
		newChannel.Reject(ssh.ConnectionFailed, "cluster not found: "+req.Cluster)
		return
	}
	ip := ""
	if cc.remote != nil {
		ip, _, _ = net.SplitHostPort(cc.remote.String())
	}
	audit := func(action string, meta map[string]interface{}) {
		meta["machine"] = machine.Name
		if err := s.store.CreateAuditLog(ctx, common.NewAuditLog(userID, action, "machine:"+machine.ID, ip, meta)); err != nil {
			s.logger.Warn("Failed to audit %s for %s: %v", action, username, err)
		}
	}

	grantKey := machine.ID + "|" + common.AccessTypeK8s
	// Skip the lookup when this connection passed it moments ago (grantCacheTTL).
	if !cc.allowed(grantKey) {
		if err := s.authService.CheckExactAccess(ctx, userID, machine.ID, common.AccessTypeK8s); err != nil {
			tracing.RecordError(span, err)
			audit("k8s.access.denied", map[string]interface{}{})
			newChannel.Reject(ssh.Prohibited, fmt.Sprintf("permission denied: no k8s access to %s; request it with access_type \"k8s\" and a namespace/role scope", machine.Name))
			return
		}
		cc.remember(grantKey)
	}
	conn, err := s.kubeAgent(machine.ID)
	if err != nil {
		newChannel.Reject(ssh.ConnectionFailed, fmt.Sprintf("%s is not available: %v", machine.Name, err))
		return
	}
	data, _ := json.Marshal(kube.Request{Op: kube.OpProxy, User: username})
	agentChannel, agentReqs, err := conn.SSHConn.Conn.OpenChannel(kube.AgentChannelType, data)
	if err != nil {
		newChannel.Reject(ssh.ConnectionFailed, "agent could not open the Kubernetes API: "+err.Error())
		return
	}
	go ssh.DiscardRequests(agentReqs)
	clientChannel, clientReqs, err := newChannel.Accept()
	if err != nil {
		agentChannel.Close()
		return
	}
	go ssh.DiscardRequests(clientReqs)

	audit("k8s.access.open", map[string]interface{}{"subject": kube.Subject(username)})
	started := time.Now()
	sent, received := relayChannels(clientChannel, agentChannel, s.timing.drain)
	audit("k8s.access.close", map[string]interface{}{
		"subject":          kube.Subject(username),
		"bytes_sent":       sent,
		"bytes_received":   received,
		"duration_seconds": int(time.Since(started).Seconds()),
	})
}
