package api

import (
	"context"
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/orion-belt-dev/orion-belt/pkg/common"
	"github.com/orion-belt-dev/orion-belt/pkg/kube"
)

// KubeReconciler issues and removes Kubernetes bindings for k8s grants. The
// gateway implements it; nil (tests, API-only setups) skips reconciling.
type KubeReconciler interface {
	ReconcileKube(ctx context.Context, machineID string, dryRun bool) (kube.Plan, error)
	KubeGrantChanged(machineID string)
}

// SetKubeReconciler wires Kubernetes reconciling after construction.
func (s *APIServer) SetKubeReconciler(r KubeReconciler) {
	s.kubeReconciler = r
}

// validateGrantTargets checks remote_users for an access type: k8s grants
// carry namespace/role scopes, rdp and vnc none, ssh and scp an account.
func validateGrantTargets(accessType string, remoteUsers []string) error {
	switch {
	case accessType == common.AccessTypeK8s:
		return kube.ValidateScopes(remoteUsers)
	case common.IsTunnelAccessType(accessType):
		return nil
	case len(remoteUsers) == 0:
		return errors.New(`remote_users is required (the target accounts the grant allows, e.g. ["root"])`)
	}
	return nil
}

// kubeGrantChanged pushes a k8s grant change to the cluster now rather than
// at the reconciler's next tick.
func (s *APIServer) kubeGrantChanged(machineID, accessType string) {
	if accessType == common.AccessTypeK8s && s.kubeReconciler != nil {
		s.kubeReconciler.KubeGrantChanged(machineID)
	}
}

// kubeReconcile reports (GET) or applies (POST) the binding changes that
// would bring a cluster in line with its k8s grants. A GET listing deletions
// is how orphaned or expired bindings are detected.
func (s *APIServer) kubeReconcile(c *gin.Context) {
	if s.kubeReconciler == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "kubernetes reconciling is not available"})
		return
	}
	id := c.Param("id")
	dryRun := c.Request.Method == http.MethodGet
	plan, err := s.kubeReconciler.ReconcileKube(c.Request.Context(), id, dryRun)
	if plan.Create == nil {
		plan.Create = []kube.Binding{}
	}
	if plan.Update == nil {
		plan.Update = []kube.Binding{}
	}
	if plan.Delete == nil {
		plan.Delete = []kube.Binding{}
	}
	if err != nil {
		code := http.StatusBadGateway
		if errors.Is(err, kube.ErrNoAgent) {
			code = http.StatusConflict
		}
		c.JSON(code, gin.H{"error": err.Error(), "plan": plan})
		return
	}
	if !dryRun {
		s.recordAudit(c, "k8s.reconcile", "machine:"+id, map[string]interface{}{
			"created": len(plan.Create), "updated": len(plan.Update), "deleted": len(plan.Delete),
		})
	}
	c.JSON(http.StatusOK, gin.H{"machine_id": id, "dry_run": dryRun, "in_sync": plan.Empty(), "plan": plan})
}
