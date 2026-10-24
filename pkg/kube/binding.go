package kube

import (
	"sort"
	"time"
)

// Labels and annotations on every binding Orion Belt issues. The managed-by
// label is how the agent lists, and is allowed to touch, only its own.
const (
	LabelManagedBy      = "app.kubernetes.io/managed-by"
	ManagedByValue      = "orion-belt"
	LabelPermission     = "orion-belt.dev/permission"
	AnnotationExpiresAt = "orion-belt.dev/expires-at"
	AnnotationUser      = "orion-belt.dev/user"
	AnnotationScope     = "orion-belt.dev/scope"
)

// Binding is a RoleBinding (Namespace set) or ClusterRoleBinding (Namespace
// empty) granting ClusterRole to Subject. Permission links it to its grant.
type Binding struct {
	Name        string     `json:"name"`
	Namespace   string     `json:"namespace,omitempty"`
	ClusterRole string     `json:"cluster_role"`
	Subject     string     `json:"subject"`
	User        string     `json:"user"` // gateway username
	Permission  string     `json:"permission_id"`
	ExpiresAt   *time.Time `json:"expires_at,omitempty"`
}

// Kind is the Kubernetes kind the binding is issued as.
func (b Binding) Kind() string {
	if b.Namespace == "" {
		return "ClusterRoleBinding"
	}
	return "RoleBinding"
}

// Scope is the namespace/role the binding grants.
func (b Binding) Scope() Scope { return Scope{Namespace: b.Namespace, Role: b.ClusterRole} }

// key identifies a binding object in the cluster.
func (b Binding) key() string { return b.Namespace + "/" + b.Name }

// Expired reports whether the binding's grant has ended at now.
func (b Binding) Expired(now time.Time) bool {
	return b.ExpiresAt != nil && !b.ExpiresAt.After(now)
}

// sameSpec reports whether a and b need no update to match: same role,
// subject and expiry. Name and namespace are already equal by key.
func sameSpec(a, b Binding) bool {
	if a.ClusterRole != b.ClusterRole || a.Subject != b.Subject || a.Permission != b.Permission {
		return false
	}
	switch {
	case a.ExpiresAt == nil || b.ExpiresAt == nil:
		return a.ExpiresAt == nil && b.ExpiresAt == nil
	default:
		return a.ExpiresAt.Unix() == b.ExpiresAt.Unix()
	}
}

// Grant is the part of a k8s permission the reconciler needs.
type Grant struct {
	PermissionID string
	Username     string
	Scopes       []string
	ExpiresAt    *time.Time
}

// Desired returns the bindings the grants call for. Scopes that do not parse
// are skipped: the API rejects them, so one here predates validation.
func Desired(grants []Grant) []Binding {
	var out []Binding
	for _, g := range grants {
		for _, raw := range g.Scopes {
			s, err := ParseScope(raw)
			if err != nil {
				continue
			}
			out = append(out, Binding{
				Name:        BindingName(g.PermissionID, s),
				Namespace:   s.Namespace,
				ClusterRole: s.Role,
				Subject:     Subject(g.Username),
				User:        g.Username,
				Permission:  g.PermissionID,
				ExpiresAt:   g.ExpiresAt,
			})
		}
	}
	return out
}

// Plan is what reconciling a cluster does: create missing bindings, update
// changed ones (an extended grant), and delete those no grant calls for.
type Plan struct {
	Create []Binding `json:"create"`
	Update []Binding `json:"update"`
	Delete []Binding `json:"delete"`
}

// Empty reports whether the cluster already matches.
func (p Plan) Empty() bool { return len(p.Create)+len(p.Update)+len(p.Delete) == 0 }

// Diff plans how to move the cluster's managed bindings, actual, to desired.
// A desired binding already past its expiry is not created; an actual one
// nothing desires is deleted, whatever the reason (expired, revoked, or an
// orphan left by a bug).
func Diff(desired, actual []Binding, now time.Time) Plan {
	have := map[string]Binding{}
	for _, b := range actual {
		have[b.key()] = b
	}
	want := map[string]bool{}
	var p Plan
	for _, b := range desired {
		if b.Expired(now) {
			continue
		}
		want[b.key()] = true
		cur, ok := have[b.key()]
		switch {
		case !ok:
			p.Create = append(p.Create, b)
		case !sameSpec(cur, b):
			p.Update = append(p.Update, b)
		}
	}
	for _, b := range actual {
		if !want[b.key()] {
			p.Delete = append(p.Delete, b)
		}
	}
	for _, list := range [][]Binding{p.Create, p.Update, p.Delete} {
		sort.Slice(list, func(i, j int) bool { return list[i].key() < list[j].key() })
	}
	return p
}
