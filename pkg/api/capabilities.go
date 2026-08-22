package api

import (
	"context"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/orion-belt-dev/orion-belt/pkg/common"
)

// Capability catalog scopes.
const (
	// capScopeRelated lists only capabilities backed by a relationship the
	// user already has: a grant, a request, a session, or a machine that
	// shares a tag with one of those.
	capScopeRelated = "related"
	// capScopeAll adds every remaining machine in the inventory.
	capScopeAll = "all"
)

// Capability statuses, from "you have it" to "you can ask for it".
const (
	capStatusGranted     = "granted"
	capStatusPending     = "pending"
	capStatusRequestable = "requestable"
)

// Capability sources, most to least specific relationship.
const (
	capSourceGrant     = "grant"
	capSourceRequest   = "request"
	capSourceSession   = "session"
	capSourceTag       = "tag"
	capSourceDirectory = "directory"
)

// Bounds on how much history the catalog is derived from, and how many
// entries it will return.
const (
	capRequestHistoryLimit = 200
	capSessionHistoryLimit = 200
	capMachineLimit        = 5000
	capMaxEntries          = 500
)

// defaultCapabilityRemoteUser is suggested for a machine the user has never
// touched, when their own history offers no better guess.
const defaultCapabilityRemoteUser = "root"

// Capability is one thing a user could ask for — a remote login on a
// machine, with a given access type — plus why it is being offered and
// whether they already hold it.
type Capability struct {
	ID            string            `json:"id"`
	MachineID     string            `json:"machine_id"`
	MachineName   string            `json:"machine_name"`
	Hostname      string            `json:"hostname,omitempty"`
	Port          int               `json:"port,omitempty"`
	MachineActive bool              `json:"machine_active"`
	Tags          map[string]string `json:"tags,omitempty"`
	// RemoteUser is the login on the target machine. Empty means the
	// relationship does not name one (any login the approver allows).
	RemoteUser   string     `json:"remote_user"`
	AccessType   string     `json:"access_type"`
	Status       string     `json:"status"`
	Source       string     `json:"source"`
	Reason       string     `json:"reason"`
	PermissionID string     `json:"permission_id,omitempty"`
	RequestID    string     `json:"request_id,omitempty"`
	ExpiresAt    *time.Time `json:"expires_at,omitempty"`
	LastUsedAt   *time.Time `json:"last_used_at,omitempty"`
}

type capabilityCatalogResponse struct {
	UserID       string       `json:"user_id"`
	Scope        string       `json:"scope"`
	GeneratedAt  string       `json:"generated_at"`
	Truncated    bool         `json:"truncated"`
	Capabilities []Capability `json:"capabilities"`
}

// capabilityInputs is everything the catalog is derived from. Kept separate
// from the handler so the ranking rules are testable without a database.
type capabilityInputs struct {
	Now         time.Time
	Scope       string
	Machines    []*common.Machine
	Permissions []*common.Permission
	Requests    []*common.AccessRequest
	Sessions    []*common.Session
}

// listCapabilities answers "what can I ask for?" for the authenticated user.
// Privileged viewers may ask on behalf of someone else with ?user_id=.
func (s *APIServer) listCapabilities(c *gin.Context) {
	ctx := c.Request.Context()

	callerID, _ := c.Get("user_id")
	userID, _ := callerID.(string)
	if requested := strings.TrimSpace(c.Query("user_id")); requested != "" && requested != userID {
		if !isPrivilegedViewer(c) {
			c.JSON(http.StatusForbidden, gin.H{"error": "admin, operator, or auditor privileges required to view another user's catalog"})
			return
		}
		userID = requested
	}
	if userID == "" {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "authentication required"})
		return
	}

	scope := strings.TrimSpace(c.Query("scope"))
	if scope == "" {
		scope = capScopeRelated
	}
	if scope != capScopeRelated && scope != capScopeAll {
		c.JSON(http.StatusBadRequest, gin.H{"error": "scope must be 'related' or 'all'"})
		return
	}

	inputs, err := s.capabilityInputsFor(ctx, userID)
	if err != nil {
		s.logger.Error("Failed to build capability catalog for user %s: %v", userID, err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to build capability catalog"})
		return
	}
	inputs.Scope = scope

	capabilities, truncated := buildCapabilities(inputs)

	c.JSON(http.StatusOK, capabilityCatalogResponse{
		UserID:       userID,
		Scope:        scope,
		GeneratedAt:  inputs.Now.Format(time.RFC3339),
		Truncated:    truncated,
		Capabilities: capabilities,
	})
}

// capabilityInputsFor collects the relationships the catalog is derived
// from. Scope is left to the caller.
func (s *APIServer) capabilityInputsFor(ctx context.Context, userID string) (capabilityInputs, error) {
	permissions, err := s.store.ListUserPermissions(ctx, userID)
	if err != nil {
		return capabilityInputs{}, fmt.Errorf("list permissions: %w", err)
	}
	requests, err := s.store.ListUserAccessRequests(ctx, userID, capRequestHistoryLimit, 0)
	if err != nil {
		return capabilityInputs{}, fmt.Errorf("list access requests: %w", err)
	}
	sessions, err := s.store.ListUserSessions(ctx, userID, capSessionHistoryLimit, 0)
	if err != nil {
		return capabilityInputs{}, fmt.Errorf("list sessions: %w", err)
	}
	machines, err := s.store.ListMachines(ctx, capMachineLimit, 0)
	if err != nil {
		return capabilityInputs{}, fmt.Errorf("list machines: %w", err)
	}

	return capabilityInputs{
		Now:         time.Now().UTC(),
		Machines:    machines,
		Permissions: permissions,
		Requests:    requests,
		Sessions:    sessions,
	}, nil
}

// buildCapabilities ranks what a user could ask for. Candidates are produced
// strongest relationship first (grant, request, session, shared tag, plain
// inventory); a weaker candidate is dropped when a stronger one already
// covers the same machine and login. Returns the catalog and whether it was
// cut at capMaxEntries.
func buildCapabilities(in capabilityInputs) ([]Capability, bool) {
	machines := make(map[string]*common.Machine, len(in.Machines))
	for _, m := range in.Machines {
		if m != nil {
			machines[m.ID] = m
		}
	}

	b := &capabilityBuilder{machines: machines, now: in.Now}

	b.addGrants(in.Permissions)
	b.addRequests(in.Requests)
	b.addSessions(in.Sessions)
	b.addTagPeers(in.Machines)
	if in.Scope == capScopeAll {
		b.addDirectory(in.Machines)
	}

	out := b.entries
	if out == nil {
		// An empty catalog is still a list, not null.
		out = []Capability{}
	}
	sort.SliceStable(out, func(i, j int) bool { return lessCapability(out[i], out[j]) })

	if len(out) > capMaxEntries {
		return out[:capMaxEntries], true
	}
	return out, false
}

type capabilityBuilder struct {
	machines map[string]*common.Machine
	now      time.Time
	entries  []Capability
	// byTarget indexes kept entries by machine+remote user so a weaker
	// candidate can be measured against what is already there.
	byTarget map[string][]Capability
	// evidence records machines the user has a first-hand relationship with;
	// it seeds the shared-tag tier and the remote-user guess.
	evidence    map[string]bool
	remoteUsage map[string]int
	remoteUsers []string
}

func (b *capabilityBuilder) add(entry Capability) {
	machine, ok := b.machines[entry.MachineID]
	if !ok {
		// A grant or session pointing at a deleted machine is not something
		// anyone can ask for.
		return
	}
	if b.byTarget == nil {
		b.byTarget = map[string][]Capability{}
	}

	entry.MachineName = machine.Name
	entry.Hostname = machine.Hostname
	entry.Port = machine.Port
	entry.MachineActive = machine.IsActive
	entry.Tags = machine.Tags
	entry.AccessType = normalizeAccessType(entry.AccessType)
	entry.RemoteUser = strings.TrimSpace(entry.RemoteUser)
	entry.ID = fmt.Sprintf("%s|%s|%s", entry.MachineID, entry.RemoteUser, entry.AccessType)

	target := entry.MachineID + "|" + entry.RemoteUser
	for _, kept := range b.byTarget[target] {
		// Candidates arrive strongest first, so anything already kept for
		// this target outranks the newcomer.
		if accessTypeCovers(kept.AccessType, entry.AccessType) {
			return
		}
		if entry.Source == capSourceTag || entry.Source == capSourceDirectory {
			// Never surface a machine the user already has a real
			// relationship with as a tag or inventory discovery.
			return
		}
	}

	b.byTarget[target] = append(b.byTarget[target], entry)
	b.entries = append(b.entries, entry)
}

// noteEvidence records a first-hand relationship with a machine and the
// remote users the user actually works as.
func (b *capabilityBuilder) noteEvidence(machineID string, remoteUsers ...string) {
	if b.evidence == nil {
		b.evidence = map[string]bool{}
		b.remoteUsage = map[string]int{}
	}
	if _, ok := b.machines[machineID]; ok {
		b.evidence[machineID] = true
	}
	for _, ru := range remoteUsers {
		ru = strings.TrimSpace(ru)
		if ru == "" {
			continue
		}
		if _, seen := b.remoteUsage[ru]; !seen {
			b.remoteUsers = append(b.remoteUsers, ru)
		}
		b.remoteUsage[ru]++
	}
}

// suggestedRemoteUser is the login to pre-fill for a machine the user has no
// history on: the one they use most often, else root.
func (b *capabilityBuilder) suggestedRemoteUser() string {
	best := ""
	bestCount := 0
	for _, ru := range b.remoteUsers {
		if n := b.remoteUsage[ru]; n > bestCount || (n == bestCount && ru < best) {
			best, bestCount = ru, n
		}
	}
	if best == "" {
		return defaultCapabilityRemoteUser
	}
	return best
}

func (b *capabilityBuilder) addGrants(permissions []*common.Permission) {
	for _, p := range permissions {
		if p == nil {
			continue
		}
		b.noteEvidence(p.MachineID, p.RemoteUsers...)
		for _, remote := range remoteUsersOrAny(p.RemoteUsers) {
			b.add(Capability{
				MachineID:    p.MachineID,
				RemoteUser:   remote,
				AccessType:   p.AccessType,
				Status:       capStatusGranted,
				Source:       capSourceGrant,
				Reason:       "You already have this grant",
				PermissionID: p.ID,
				ExpiresAt:    p.ExpiresAt,
			})
		}
	}
}

func (b *capabilityBuilder) addRequests(requests []*common.AccessRequest) {
	// Pending asks outrank finished ones: a user needs to see that a request
	// is already in flight before being offered the same thing again.
	for _, wantPending := range []bool{true, false} {
		for _, r := range requests {
			if r == nil || (r.Status == "pending") != wantPending {
				continue
			}
			b.noteEvidence(r.MachineID, r.RemoteUsers...)
			status := capStatusRequestable
			if wantPending {
				status = capStatusPending
			}
			for _, remote := range remoteUsersOrAny(r.RemoteUsers) {
				b.add(Capability{
					MachineID:  r.MachineID,
					RemoteUser: remote,
					AccessType: r.AccessType,
					Status:     status,
					Source:     capSourceRequest,
					Reason:     requestReason(r, b.now),
					RequestID:  r.ID,
				})
			}
		}
	}
}

func (b *capabilityBuilder) addSessions(sessions []*common.Session) {
	// Newest session per machine+login wins the "last used" stamp.
	type sessionKey struct{ machineID, remoteUser string }
	lastUsed := map[sessionKey]time.Time{}
	order := make([]sessionKey, 0, len(sessions))
	for _, s := range sessions {
		if s == nil {
			continue
		}
		b.noteEvidence(s.MachineID, s.RemoteUser)
		key := sessionKey{s.MachineID, strings.TrimSpace(s.RemoteUser)}
		if prev, ok := lastUsed[key]; !ok {
			order = append(order, key)
			lastUsed[key] = s.StartTime
		} else if s.StartTime.After(prev) {
			lastUsed[key] = s.StartTime
		}
	}

	for _, key := range order {
		when := lastUsed[key]
		b.add(Capability{
			MachineID:  key.machineID,
			RemoteUser: key.remoteUser,
			AccessType: "both",
			Status:     capStatusRequestable,
			Source:     capSourceSession,
			Reason:     "You have connected here before",
			LastUsedAt: &when,
		})
	}
}

// addTagPeers offers machines that share a tag with one the user already has
// a relationship with — the usual shape of "I can reach web-1, so web-2 is
// probably mine to ask for too".
func (b *capabilityBuilder) addTagPeers(machines []*common.Machine) {
	origins := b.tagOrigins(machines)
	if len(origins) == 0 {
		return
	}

	remote := b.suggestedRemoteUser()
	for _, m := range byName(machines) {
		if b.evidence[m.ID] {
			continue
		}
		for _, k := range sortedTagKeys(m.Tags) {
			tag := k + "=" + m.Tags[k]
			origin, ok := origins[tag]
			if !ok {
				continue
			}
			b.add(Capability{
				MachineID:  m.ID,
				RemoteUser: remote,
				AccessType: "both",
				Status:     capStatusRequestable,
				Source:     capSourceTag,
				Reason:     fmt.Sprintf("Shares %s with %s", tag, origin),
			})
			break
		}
	}
}

// tagOrigins maps each tag on a machine the user already knows to the name of
// the machine it is borrowed from — the "…with app-01" half of the reason.
func (b *capabilityBuilder) tagOrigins(machines []*common.Machine) map[string]string {
	if len(b.evidence) == 0 {
		return nil
	}
	origins := map[string]string{}
	for _, m := range machines {
		if m == nil || !b.evidence[m.ID] {
			continue
		}
		for k, v := range m.Tags {
			tag := k + "=" + v
			// Deterministic origin when several known machines share a tag.
			if prev, ok := origins[tag]; !ok || m.Name < prev {
				origins[tag] = m.Name
			}
		}
	}
	return origins
}

func (b *capabilityBuilder) addDirectory(machines []*common.Machine) {
	remote := b.suggestedRemoteUser()
	for _, m := range byName(machines) {
		b.add(Capability{
			MachineID:  m.ID,
			RemoteUser: remote,
			AccessType: "both",
			Status:     capStatusRequestable,
			Source:     capSourceDirectory,
			Reason:     "Listed in the machine inventory",
		})
	}
}

// byName drops nils and orders machines by name, so a catalog built twice
// from the same data reads the same way.
func byName(machines []*common.Machine) []*common.Machine {
	out := make([]*common.Machine, 0, len(machines))
	for _, m := range machines {
		if m != nil {
			out = append(out, m)
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

func requestReason(r *common.AccessRequest, now time.Time) string {
	switch r.Status {
	case "pending":
		return "You have a request awaiting approval"
	case "approved":
		if r.ExpiresAt != nil && r.ExpiresAt.Before(now) {
			return "Your approved access has expired"
		}
		return "Approved for you before"
	case "rejected":
		return "A previous request was rejected"
	case "expired":
		return "An earlier request expired"
	}
	return "You have requested this before"
}

// remoteUsersOrAny keeps a relationship that names no login usable: it
// becomes a single entry with an empty remote user, meaning "any".
func remoteUsersOrAny(remoteUsers []string) []string {
	out := make([]string, 0, len(remoteUsers))
	for _, ru := range remoteUsers {
		if ru = strings.TrimSpace(ru); ru != "" {
			out = append(out, ru)
		}
	}
	if len(out) == 0 {
		return []string{""}
	}
	return out
}

func normalizeAccessType(accessType string) string {
	switch strings.ToLower(strings.TrimSpace(accessType)) {
	case "ssh":
		return "ssh"
	case "scp":
		return "scp"
	default:
		return "both"
	}
}

// accessTypeCovers reports whether holding a makes asking for b pointless.
func accessTypeCovers(a, b string) bool {
	return a == "both" || a == b
}

func sortedTagKeys(tags map[string]string) []string {
	keys := make([]string, 0, len(tags))
	for k := range tags {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func capStatusRank(status string) int {
	switch status {
	case capStatusGranted:
		return 0
	case capStatusPending:
		return 1
	default:
		return 2
	}
}

func capSourceRank(source string) int {
	switch source {
	case capSourceGrant:
		return 0
	case capSourceRequest:
		return 1
	case capSourceSession:
		return 2
	case capSourceTag:
		return 3
	default:
		return 4
	}
}

func lessCapability(a, b Capability) bool {
	if ra, rb := capStatusRank(a.Status), capStatusRank(b.Status); ra != rb {
		return ra < rb
	}
	if ra, rb := capSourceRank(a.Source), capSourceRank(b.Source); ra != rb {
		return ra < rb
	}
	if a.MachineName != b.MachineName {
		return a.MachineName < b.MachineName
	}
	if a.RemoteUser != b.RemoteUser {
		return a.RemoteUser < b.RemoteUser
	}
	return a.AccessType < b.AccessType
}
