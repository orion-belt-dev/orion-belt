// Package kube issues just-in-time Kubernetes access: an approved k8s grant
// becomes a RoleBinding (or ClusterRoleBinding) for the user, created and
// removed by the cluster's in-cluster agent. The gateway decides what should
// exist; the agent talks to the API server.
package kube

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"regexp"
	"strings"
)

// AllNamespaces is the namespace part of a cluster-wide scope, "*/role",
// which is issued as a ClusterRoleBinding.
const AllNamespaces = "*"

// SubjectPrefix namespaces the Kubernetes users the agent impersonates, so a
// binding issued here can never match an identity from another authenticator.
const SubjectPrefix = "orion:"

// Subject is the Kubernetes user a gateway user acts as.
func Subject(username string) string { return SubjectPrefix + username }

var (
	// RFC 1123 label, as Kubernetes requires of namespace names.
	namespaceRE = regexp.MustCompile(`^[a-z0-9]([-a-z0-9]*[a-z0-9])?$`)
	// ClusterRole names are path segments; built-in ones use ':' (system:*).
	roleRE = regexp.MustCompile(`^[a-z0-9]([-a-z0-9.:]*[a-z0-9])?$`)
)

// Scope is one namespace/role pair a k8s grant allows. A grant stores its
// scopes in remote_users, so they flow through requests, approval, explain
// and history unchanged.
type Scope struct {
	Namespace string // "" for cluster-wide
	Role      string // ClusterRole name
}

// ParseScope parses "namespace/role" or "*/role".
func ParseScope(s string) (Scope, error) {
	ns, role, ok := strings.Cut(strings.TrimSpace(s), "/")
	if !ok || ns == "" || role == "" {
		return Scope{}, fmt.Errorf("k8s scope %q: want namespace/role or */role", s)
	}
	if ns != AllNamespaces && (len(ns) > 63 || !namespaceRE.MatchString(ns)) {
		return Scope{}, fmt.Errorf("k8s scope %q: %q is not a valid namespace", s, ns)
	}
	if len(role) > 253 || !roleRE.MatchString(role) {
		return Scope{}, fmt.Errorf("k8s scope %q: %q is not a valid ClusterRole name", s, role)
	}
	if ns == AllNamespaces {
		ns = ""
	}
	return Scope{Namespace: ns, Role: role}, nil
}

// ValidateScopes checks every scope of a k8s grant or request.
func ValidateScopes(scopes []string) error {
	if len(scopes) == 0 {
		return fmt.Errorf("a k8s grant needs at least one namespace/role scope, e.g. payments/edit or */view")
	}
	for _, s := range scopes {
		if _, err := ParseScope(s); err != nil {
			return err
		}
	}
	return nil
}

// String is the stored form, "namespace/role" or "*/role".
func (s Scope) String() string {
	ns := s.Namespace
	if ns == "" {
		ns = AllNamespaces
	}
	return ns + "/" + s.Role
}

// ClusterWide reports whether the scope is issued as a ClusterRoleBinding.
func (s Scope) ClusterWide() bool { return s.Namespace == "" }

// BindingName is the name of the binding issued for one scope of a grant.
// It is stable, so reconciling again finds the same object, and it differs
// per scope, so one grant can bind several namespaces.
func BindingName(permissionID string, s Scope) string {
	sum := sha256.Sum256([]byte(s.String()))
	id := permissionID
	if len(id) > 8 {
		id = id[:8]
	}
	return "orion-" + id + "-" + hex.EncodeToString(sum[:])[:8]
}
