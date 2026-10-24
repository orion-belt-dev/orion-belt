package kube

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
)

// ErrNoAgent is returned for a cluster whose in-cluster agent is not
// connected (or a machine that is not a cluster).
var ErrNoAgent = errors.New("no connected in-cluster Kubernetes agent")

// SSH names used between osh, the gateway and an in-cluster agent.
const (
	// AgentChannelType is opened by the gateway to an in-cluster agent. Its
	// extra data is a JSON Request.
	AgentChannelType = "orion-k8s"
	// ClientChannelType is opened by osh to the gateway for kubectl traffic.
	// Its extra data is ClientChannelData.
	ClientChannelType = "orion-kube@orion-belt.dev"
	// CapabilityRequest is the global request an in-cluster agent sends after
	// connecting, so the gateway knows to reconcile it.
	CapabilityRequest = "orion-k8s@orion-belt.dev"
)

// Agent channel operations.
const (
	OpList   = "list"   // reply: Response.Bindings
	OpApply  = "apply"  // Request.Binding
	OpDelete = "delete" // Request.Binding
	OpProxy  = "proxy"  // the channel then carries HTTP for Request.User
)

// Request is one gateway-to-agent operation.
type Request struct {
	Op      string   `json:"op"`
	User    string   `json:"user,omitempty"` // proxy: gateway username to impersonate
	Binding *Binding `json:"binding,omitempty"`
}

// Response answers list, apply and delete.
type Response struct {
	Bindings []Binding `json:"bindings,omitempty"`
	Error    string    `json:"error,omitempty"`
}

// ClientChannelData names the cluster a kubectl connection is for.
type ClientChannelData struct {
	Cluster string
}

// WriteResponse sends r and returns err's message to the caller, if any.
func WriteResponse(w io.Writer, bindings []Binding, err error) error {
	r := Response{Bindings: bindings}
	if err != nil {
		r.Error = err.Error()
	}
	return json.NewEncoder(w).Encode(r)
}

// ReadResponse reads an agent's answer, turning a reported error into one.
func ReadResponse(r io.Reader) ([]Binding, error) {
	var resp Response
	if err := json.NewDecoder(io.LimitReader(r, 16<<20)).Decode(&resp); err != nil {
		return nil, fmt.Errorf("read agent response: %w", err)
	}
	if resp.Error != "" {
		return nil, fmt.Errorf("agent: %s", resp.Error)
	}
	return resp.Bindings, nil
}
