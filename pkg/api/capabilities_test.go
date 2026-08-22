package api

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/orion-belt-dev/orion-belt/pkg/common"
	"github.com/orion-belt-dev/orion-belt/pkg/database"
)

var capNow = time.Date(2026, 8, 6, 12, 0, 0, 0, time.UTC)

func capMachines() []*common.Machine {
	return []*common.Machine{
		{ID: "m1", Name: "app-01", Hostname: "app-01.internal", Port: 22, IsActive: true, Tags: map[string]string{"env": "prod"}},
		{ID: "m2", Name: "app-02", Hostname: "app-02.internal", Port: 22, IsActive: true, Tags: map[string]string{"env": "prod"}},
		{ID: "m3", Name: "db-01", Hostname: "db-01.internal", Port: 22, IsActive: false, Tags: map[string]string{"env": "staging"}},
	}
}

func findCapability(caps []Capability, machineID, remoteUser string) (Capability, bool) {
	for _, c := range caps {
		if c.MachineID == machineID && c.RemoteUser == remoteUser {
			return c, true
		}
	}
	return Capability{}, false
}

// An active grant is the strongest relationship there is: it heads the
// catalog, and the console needs the permission id and expiry to say so.
func TestBuildCapabilitiesSurfacesActiveGrants(t *testing.T) {
	expires := capNow.Add(2 * time.Hour)
	caps, _ := buildCapabilities(capabilityInputs{
		Now:      capNow,
		Scope:    capScopeRelated,
		Machines: capMachines(),
		Permissions: []*common.Permission{
			{ID: "p1", MachineID: "m1", AccessType: "ssh", RemoteUsers: []string{"root", "deploy"}, ExpiresAt: &expires},
		},
	})

	if len(caps) < 2 {
		t.Fatalf("expected an entry per remote user, got %d: %+v", len(caps), caps)
	}
	got, ok := findCapability(caps, "m1", "deploy")
	if !ok {
		t.Fatalf("expected a capability for deploy@app-01, got %+v", caps)
	}
	if got.Status != capStatusGranted || got.Source != capSourceGrant {
		t.Errorf("expected a granted/grant entry, got %s/%s", got.Status, got.Source)
	}
	if got.PermissionID != "p1" {
		t.Errorf("expected the permission id to be carried, got %q", got.PermissionID)
	}
	if got.ExpiresAt == nil || !got.ExpiresAt.Equal(expires) {
		t.Errorf("expected the grant expiry to be carried, got %v", got.ExpiresAt)
	}
	if got.MachineName != "app-01" || got.Hostname != "app-01.internal" || got.Port != 22 {
		t.Errorf("expected the machine to be resolved, got %+v", got)
	}
	if got.AccessType != "ssh" {
		t.Errorf("expected the granted access type, got %q", got.AccessType)
	}
}

// A grant that names no remote user still has to be listed — as "any" —
// rather than vanishing from the catalog.
func TestBuildCapabilitiesGrantWithoutRemoteUsers(t *testing.T) {
	caps, _ := buildCapabilities(capabilityInputs{
		Now:         capNow,
		Scope:       capScopeRelated,
		Machines:    capMachines(),
		Permissions: []*common.Permission{{ID: "p1", MachineID: "m1", AccessType: "both"}},
	})

	got, ok := findCapability(caps, "m1", "")
	if !ok {
		t.Fatalf("expected the grant to be listed with an empty (any) remote user, got %+v", caps)
	}
	if got.Status != capStatusGranted {
		t.Errorf("expected a granted entry, got %s", got.Status)
	}
}

// A grant or session against a machine that has since been deleted is not
// something anyone can ask for.
func TestBuildCapabilitiesSkipsUnknownMachines(t *testing.T) {
	caps, _ := buildCapabilities(capabilityInputs{
		Now:         capNow,
		Scope:       capScopeAll,
		Machines:    capMachines(),
		Permissions: []*common.Permission{{ID: "p1", MachineID: "gone", RemoteUsers: []string{"root"}}},
		Sessions:    []*common.Session{{ID: "s1", MachineID: "gone", RemoteUser: "root", StartTime: capNow}},
	})

	if _, ok := findCapability(caps, "gone", "root"); ok {
		t.Fatalf("expected the deleted machine to be dropped, got %+v", caps)
	}
}

// A request in flight must be visible as pending, so the user asks once
// rather than filing a duplicate.
func TestBuildCapabilitiesMarksPendingRequests(t *testing.T) {
	caps, _ := buildCapabilities(capabilityInputs{
		Now:      capNow,
		Scope:    capScopeRelated,
		Machines: capMachines(),
		Requests: []*common.AccessRequest{
			{ID: "r1", MachineID: "m1", RemoteUsers: []string{"root"}, AccessType: "both", Status: "pending", RequestedAt: capNow.Add(-time.Hour)},
		},
		Sessions: []*common.Session{
			{ID: "s1", MachineID: "m1", RemoteUser: "root", StartTime: capNow.Add(-48 * time.Hour)},
		},
	})

	onM1 := 0
	for _, c := range caps {
		if c.MachineID == "m1" {
			onM1++
		}
	}
	if onM1 != 1 {
		t.Fatalf("expected the session to be folded into the pending ask, got %d entries for app-01: %+v", onM1, caps)
	}
	got, _ := findCapability(caps, "m1", "root")
	if got.Status != capStatusPending || got.RequestID != "r1" {
		t.Errorf("expected a pending entry carrying the request id, got %+v", got)
	}
}

// Lapsed access is the most likely thing a user wants back, and the reason
// has to say so rather than leaving them guessing.
func TestBuildCapabilitiesExplainsExpiredAccess(t *testing.T) {
	expired := capNow.Add(-3 * time.Hour)
	caps, _ := buildCapabilities(capabilityInputs{
		Now:      capNow,
		Scope:    capScopeRelated,
		Machines: capMachines(),
		Requests: []*common.AccessRequest{
			{ID: "r1", MachineID: "m1", RemoteUsers: []string{"root"}, AccessType: "ssh", Status: "approved", ExpiresAt: &expired},
		},
	})

	got, ok := findCapability(caps, "m1", "root")
	if !ok {
		t.Fatalf("expected the lapsed access to be offered again, got %+v", caps)
	}
	if got.Status != capStatusRequestable || got.Source != capSourceRequest {
		t.Errorf("expected a requestable/request entry, got %s/%s", got.Status, got.Source)
	}
	if got.Reason != "Your approved access has expired" {
		t.Errorf("unexpected reason: %q", got.Reason)
	}
}

// Somewhere the user has actually worked is a capability they can name, even
// with no grant or request on file.
func TestBuildCapabilitiesUsesSessionHistory(t *testing.T) {
	older := capNow.Add(-72 * time.Hour)
	newer := capNow.Add(-4 * time.Hour)
	caps, _ := buildCapabilities(capabilityInputs{
		Now:      capNow,
		Scope:    capScopeRelated,
		Machines: capMachines(),
		Sessions: []*common.Session{
			{ID: "s1", MachineID: "m3", RemoteUser: "postgres", StartTime: older},
			{ID: "s2", MachineID: "m3", RemoteUser: "postgres", StartTime: newer},
		},
	})

	got, ok := findCapability(caps, "m3", "postgres")
	if !ok {
		t.Fatalf("expected a capability from session history, got %+v", caps)
	}
	if got.Source != capSourceSession || got.Status != capStatusRequestable {
		t.Errorf("expected a requestable/session entry, got %s/%s", got.Status, got.Source)
	}
	if got.LastUsedAt == nil || !got.LastUsedAt.Equal(newer) {
		t.Errorf("expected the most recent session to stamp last used, got %v", got.LastUsedAt)
	}
	if got.MachineActive {
		t.Error("expected the offline machine to be reported as inactive")
	}
}

// Discovery is the point of the catalog: a machine tagged like one the user
// already reaches is offered, named after the machine it resembles.
func TestBuildCapabilitiesOffersTagPeers(t *testing.T) {
	caps, _ := buildCapabilities(capabilityInputs{
		Now:         capNow,
		Scope:       capScopeRelated,
		Machines:    capMachines(),
		Permissions: []*common.Permission{{ID: "p1", MachineID: "m1", AccessType: "both", RemoteUsers: []string{"deploy"}}},
	})

	got, ok := findCapability(caps, "m2", "deploy")
	if !ok {
		t.Fatalf("expected the tag peer to be offered with the login the user works as, got %+v", caps)
	}
	if got.Source != capSourceTag || got.Status != capStatusRequestable {
		t.Errorf("expected a requestable/tag entry, got %s/%s", got.Status, got.Source)
	}
	if got.Reason != "Shares env=prod with app-01" {
		t.Errorf("unexpected reason: %q", got.Reason)
	}
	if _, ok := findCapability(caps, "m3", "deploy"); ok {
		t.Error("expected a machine with no shared tag to stay out of the related scope")
	}
}

// A machine the user already has a real relationship with must not be
// re-offered as a tag or inventory discovery.
func TestBuildCapabilitiesDoesNotRediscoverKnownMachines(t *testing.T) {
	caps, _ := buildCapabilities(capabilityInputs{
		Now:         capNow,
		Scope:       capScopeAll,
		Machines:    capMachines(),
		Permissions: []*common.Permission{{ID: "p1", MachineID: "m1", AccessType: "ssh", RemoteUsers: []string{"root"}}},
	})

	for _, c := range caps {
		if c.MachineID == "m1" && c.Source != capSourceGrant {
			t.Errorf("expected app-01 to appear only as a grant, got a %s entry: %+v", c.Source, c)
		}
	}
}

// The default scope is the filtered one; the whole inventory is opt-in.
func TestBuildCapabilitiesScopesTheInventory(t *testing.T) {
	related, _ := buildCapabilities(capabilityInputs{
		Now:      capNow,
		Scope:    capScopeRelated,
		Machines: capMachines(),
	})
	if len(related) != 0 {
		t.Fatalf("expected no capabilities without a relationship, got %+v", related)
	}

	all, _ := buildCapabilities(capabilityInputs{
		Now:      capNow,
		Scope:    capScopeAll,
		Machines: capMachines(),
	})
	if len(all) != 3 {
		t.Fatalf("expected every machine in the all scope, got %d: %+v", len(all), all)
	}
	for _, c := range all {
		if c.Source != capSourceDirectory || c.RemoteUser != defaultCapabilityRemoteUser {
			t.Errorf("expected an inventory entry defaulting to root, got %+v", c)
		}
	}
}

// Ordering is what makes the list readable: what you hold, then what you are
// waiting on, then what you can ask for.
func TestBuildCapabilitiesOrdersByStatusThenSource(t *testing.T) {
	caps, _ := buildCapabilities(capabilityInputs{
		Now:         capNow,
		Scope:       capScopeAll,
		Machines:    capMachines(),
		Permissions: []*common.Permission{{ID: "p1", MachineID: "m3", AccessType: "ssh", RemoteUsers: []string{"postgres"}}},
		Requests: []*common.AccessRequest{
			{ID: "r1", MachineID: "m1", RemoteUsers: []string{"root"}, AccessType: "both", Status: "pending"},
		},
		Sessions: []*common.Session{
			{ID: "s1", MachineID: "m2", RemoteUser: "deploy", StartTime: capNow.Add(-time.Hour)},
		},
	})

	var order []string
	for _, c := range caps {
		order = append(order, c.Status+"/"+c.Source)
	}
	want := []string{
		capStatusGranted + "/" + capSourceGrant,
		capStatusPending + "/" + capSourceRequest,
		capStatusRequestable + "/" + capSourceSession,
	}
	if len(order) < len(want) {
		t.Fatalf("expected at least %d entries, got %v", len(want), order)
	}
	for i, w := range want {
		if order[i] != w {
			t.Errorf("position %d: expected %s, got %s (full order %v)", i, w, order[i], order)
		}
	}
}

// A large inventory must not turn the catalog into an unbounded response.
func TestBuildCapabilitiesTruncatesLargeCatalogs(t *testing.T) {
	machines := make([]*common.Machine, 0, capMaxEntries+10)
	for i := 0; i < capMaxEntries+10; i++ {
		machines = append(machines, &common.Machine{ID: fmt.Sprintf("m%04d", i), Name: fmt.Sprintf("host-%04d", i), IsActive: true})
	}

	caps, truncated := buildCapabilities(capabilityInputs{Now: capNow, Scope: capScopeAll, Machines: machines})

	if !truncated {
		t.Error("expected the catalog to report truncation")
	}
	if len(caps) != capMaxEntries {
		t.Fatalf("expected the catalog capped at %d, got %d", capMaxEntries, len(caps))
	}
}

// The pre-filled login for an unfamiliar machine should be the one the user
// actually works as, not a guess.
func TestBuildCapabilitiesSuggestsTheUsersUsualLogin(t *testing.T) {
	caps, _ := buildCapabilities(capabilityInputs{
		Now:      capNow,
		Scope:    capScopeAll,
		Machines: capMachines(),
		Sessions: []*common.Session{
			{ID: "s1", MachineID: "m1", RemoteUser: "ubuntu", StartTime: capNow.Add(-time.Hour)},
			{ID: "s2", MachineID: "m1", RemoteUser: "ubuntu", StartTime: capNow.Add(-2 * time.Hour)},
			{ID: "s3", MachineID: "m3", RemoteUser: "postgres", StartTime: capNow.Add(-3 * time.Hour)},
		},
	})

	got, ok := findCapability(caps, "m2", "ubuntu")
	if !ok {
		t.Fatalf("expected the unfamiliar machine to be offered as ubuntu, got %+v", caps)
	}
	if got.Source != capSourceTag {
		t.Errorf("expected app-02 to come from the shared tag, got %s", got.Source)
	}
}

// capabilityStore stands in for the read side of the catalog. It embeds the
// interface so any method the handler calls but this test does not stub is a
// compile-time-visible panic rather than silent behaviour.
type capabilityStore struct {
	database.Store

	userID      string
	permissions []*common.Permission
	requests    []*common.AccessRequest
	sessions    []*common.Session
	machines    []*common.Machine
}

func (s *capabilityStore) ListUserPermissions(_ context.Context, userID string) ([]*common.Permission, error) {
	s.userID = userID
	return s.permissions, nil
}

func (s *capabilityStore) ListUserAccessRequests(_ context.Context, userID string, _, _ int) ([]*common.AccessRequest, error) {
	s.userID = userID
	return s.requests, nil
}

func (s *capabilityStore) ListUserSessions(_ context.Context, userID string, _, _ int) ([]*common.Session, error) {
	s.userID = userID
	return s.sessions, nil
}

func (s *capabilityStore) ListMachines(context.Context, int, int) ([]*common.Machine, error) {
	return s.machines, nil
}

func capabilityContext(t *testing.T, rawQuery string, keys map[string]any) (*gin.Context, *httptest.ResponseRecorder) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodGet, "/api/v1/capabilities?"+rawQuery, nil)
	for k, v := range keys {
		c.Set(k, v)
	}
	return c, rec
}

func TestListCapabilitiesScopesToTheCaller(t *testing.T) {
	store := &capabilityStore{
		machines:    capMachines(),
		permissions: []*common.Permission{{ID: "p1", MachineID: "m1", AccessType: "ssh", RemoteUsers: []string{"root"}}},
	}
	s := &APIServer{store: store}

	c, rec := capabilityContext(t, "", map[string]any{"user_id": "u1", "role": common.RoleUser})
	s.listCapabilities(c)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d (%s)", rec.Code, rec.Body.String())
	}
	body := decodeBody[capabilityCatalogResponse](t, rec)
	if body.UserID != "u1" || store.userID != "u1" {
		t.Errorf("expected the caller's own catalog, got %q (store queried %q)", body.UserID, store.userID)
	}
	if body.Scope != capScopeRelated {
		t.Errorf("expected the related scope by default, got %q", body.Scope)
	}
	if len(body.Capabilities) == 0 {
		t.Fatal("expected the caller's grant to be listed")
	}
}

// Another user's catalog says who can reach what, so a plain user must not be
// able to read one.
func TestListCapabilitiesRejectsBorrowedUserID(t *testing.T) {
	s := &APIServer{store: &capabilityStore{machines: capMachines()}}

	c, rec := capabilityContext(t, "user_id=someone-else", map[string]any{"user_id": "u1", "role": common.RoleUser})
	s.listCapabilities(c)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected 403 for a plain user reading another catalog, got %d (%s)", rec.Code, rec.Body.String())
	}
}

func TestListCapabilitiesAllowsPrivilegedViewerToInspectAnotherUser(t *testing.T) {
	store := &capabilityStore{machines: capMachines()}
	s := &APIServer{store: store}

	c, rec := capabilityContext(t, "user_id=u2&scope=all", map[string]any{"user_id": "admin-1", "role": common.RoleAdmin})
	s.listCapabilities(c)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d (%s)", rec.Code, rec.Body.String())
	}
	body := decodeBody[capabilityCatalogResponse](t, rec)
	if body.UserID != "u2" || store.userID != "u2" {
		t.Errorf("expected u2's catalog, got %q (store queried %q)", body.UserID, store.userID)
	}
	if body.Scope != capScopeAll || len(body.Capabilities) != len(capMachines()) {
		t.Errorf("expected the full inventory for u2, got scope %q with %d entries", body.Scope, len(body.Capabilities))
	}
}

func TestListCapabilitiesRejectsUnknownScope(t *testing.T) {
	s := &APIServer{store: &capabilityStore{machines: capMachines()}}

	c, rec := capabilityContext(t, "scope=everything", map[string]any{"user_id": "u1", "role": common.RoleUser})
	s.listCapabilities(c)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for an unknown scope, got %d (%s)", rec.Code, rec.Body.String())
	}
}
