package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/orion-belt-dev/orion-belt/pkg/kube"
	gossh "golang.org/x/crypto/ssh"
)

// kubeSweepInterval is how often an in-cluster agent deletes bindings whose
// grant has expired, on its own, so access ends on time even while the
// gateway is unreachable. A variable so tests need not wait.
var kubeSweepInterval = time.Minute

// kubeAgent is the in-cluster half of the Kubernetes integration.
type kubeAgent struct {
	client *kube.Client
	once   sync.Once
}

// kubeClient returns the API server client, building it on first use. Nil
// means the agent is not configured as an in-cluster agent.
func (a *Agent) kubeClient() (*kube.Client, error) {
	cfg := a.config.Agent.Kubernetes
	if !cfg.Enabled {
		return nil, nil
	}
	var err error
	a.kube.once.Do(func() {
		a.kube.client, err = kube.NewClient(kube.Config{APIServer: cfg.APIServer, TokenFile: cfg.TokenFile, CAFile: cfg.CAFile})
	})
	if err != nil {
		return nil, err
	}
	if a.kube.client == nil {
		return nil, fmt.Errorf("kubernetes client unavailable")
	}
	return a.kube.client, nil
}

// announceKubernetes tells the gateway this agent fronts a cluster, so it
// reconciles the cluster's bindings now and on its loop.
func (a *Agent) announceKubernetes(conn gossh.Conn) {
	c, err := a.kubeClient()
	if err != nil {
		a.logger.Error("Kubernetes enabled but not usable: %v", err)
		return
	}
	if c == nil {
		return
	}
	if _, _, err := conn.SendRequest(kube.CapabilityRequest, false, nil); err != nil {
		a.logger.Warn("Announcing Kubernetes to the gateway: %v", err)
	}
}

// handleKube serves one gateway operation on the cluster: list, apply or
// delete an Orion Belt binding, or proxy kubectl traffic as a user.
func (a *Agent) handleKube(newChannel gossh.NewChannel) {
	var req kube.Request
	if err := json.Unmarshal(newChannel.ExtraData(), &req); err != nil {
		newChannel.Reject(gossh.ConnectionFailed, "malformed kubernetes request")
		return
	}
	c, err := a.kubeClient()
	if err != nil || c == nil {
		newChannel.Reject(gossh.Prohibited, "kubernetes is not enabled on this agent")
		return
	}
	if req.Op == kube.OpProxy && req.User == "" {
		newChannel.Reject(gossh.Prohibited, "proxy needs a user")
		return
	}

	channel, reqs, err := newChannel.Accept()
	if err != nil {
		return
	}
	defer channel.Close()
	go gossh.DiscardRequests(reqs)

	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	switch req.Op {
	case kube.OpProxy:
		cancel() // the proxy lives as long as the kubectl connection
		a.logger.Info("Kubernetes API session for %s", req.User)
		kube.ServeStream(channel, kube.NewProxy(c, req.User, func(err error) {
			a.logger.Warn("Kubernetes proxy for %s: %v", req.User, err)
		}))
		return
	case kube.OpList:
		bindings, err := c.List(ctx)
		_ = kube.WriteResponse(channel, bindings, err)
	case kube.OpApply, kube.OpDelete:
		if req.Binding == nil {
			_ = kube.WriteResponse(channel, nil, fmt.Errorf("%s needs a binding", req.Op))
			break
		}
		if req.Op == kube.OpApply {
			err = c.Apply(ctx, *req.Binding)
		} else {
			err = c.Delete(ctx, *req.Binding)
		}
		if err == nil {
			a.logger.Info("Kubernetes %s %s %s/%s", req.Op, req.Binding.Kind(), req.Binding.Namespace, req.Binding.Name)
		}
		_ = kube.WriteResponse(channel, nil, err)
	default:
		_ = kube.WriteResponse(channel, nil, fmt.Errorf("unknown operation %q", req.Op))
	}
	_ = channel.CloseWrite()
}

// runKubeSweep deletes expired Orion Belt bindings until ctx ends.
func (a *Agent) runKubeSweep(ctx context.Context) {
	ticker := time.NewTicker(kubeSweepInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			a.sweepKube(ctx)
		}
	}
}

// sweepKube is one pass: delete every managed binding past its expiry.
func (a *Agent) sweepKube(ctx context.Context) int {
	c, err := a.kubeClient()
	if err != nil || c == nil {
		return 0
	}
	bindings, err := c.List(ctx)
	if err != nil {
		a.logger.Warn("Kubernetes sweep: %v", err)
		return 0
	}
	deleted := 0
	now := time.Now()
	for _, b := range bindings {
		if !b.Expired(now) {
			continue
		}
		if err := c.Delete(ctx, b); err != nil {
			a.logger.Warn("Kubernetes sweep: delete %s: %v", b.Name, err)
			continue
		}
		a.logger.Info("Kubernetes sweep: deleted expired %s %s/%s for %s", b.Kind(), b.Namespace, b.Name, b.User)
		deleted++
	}
	return deleted
}
