package sdk

import (
	"context"
	"net/http"
	"time"
)

// KubeBinding is a RoleBinding (Namespace set) or ClusterRoleBinding issued
// for a k8s grant.
type KubeBinding struct {
	Name         string     `json:"name"`
	Namespace    string     `json:"namespace,omitempty"`
	ClusterRole  string     `json:"cluster_role"`
	Subject      string     `json:"subject"`
	User         string     `json:"user"`
	PermissionID string     `json:"permission_id"`
	ExpiresAt    *time.Time `json:"expires_at,omitempty"`
}

// KubePlan lists the binding changes a reconcile made or would make.
type KubePlan struct {
	Create []KubeBinding `json:"create"`
	Update []KubeBinding `json:"update"`
	Delete []KubeBinding `json:"delete"`
}

// KubeReconcileResult is the answer of the reconcile endpoints.
type KubeReconcileResult struct {
	MachineID string   `json:"machine_id"`
	DryRun    bool     `json:"dry_run"`
	InSync    bool     `json:"in_sync"`
	Plan      KubePlan `json:"plan"`
}

// KubeReconcile reconciles a cluster's bindings with its k8s grants (admin
// endpoint). With dryRun it only reports the drift: missing bindings, and
// orphaned or expired ones that would be deleted.
func (c *Client) KubeReconcile(ctx context.Context, machineID string, dryRun bool) (*KubeReconcileResult, error) {
	method := http.MethodPost
	if dryRun {
		method = http.MethodGet
	}
	var out KubeReconcileResult
	if err := c.Do(ctx, method, "/admin/machines/"+machineID+"/kubernetes/reconcile", nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}
