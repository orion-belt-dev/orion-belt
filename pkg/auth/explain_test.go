package auth

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/orion-belt-dev/orion-belt/pkg/common"
	"github.com/orion-belt-dev/orion-belt/pkg/database"
)

// explainStore adds the reads ExplainAccess needs to memStore.
type explainStore struct {
	*memStore
	machine  *common.Machine
	grants   []*common.Permission
	requests []*common.AccessRequest
}

func (s *explainStore) GetMachine(_ context.Context, id string) (*common.Machine, error) {
	if err := s.fail["GetMachine"]; err != nil {
		return nil, err
	}
	if s.machine != nil && s.machine.ID == id {
		return s.machine, nil
	}
	return nil, database.ErrNotFound
}

func (s *explainStore) ListPairPermissions(context.Context, string, string) ([]*common.Permission, error) {
	return s.grants, s.fail["ListPairPermissions"]
}

func (s *explainStore) ListUserAccessRequests(context.Context, string, int, int) ([]*common.AccessRequest, error) {
	return s.requests, s.fail["ListUserAccessRequests"]
}

type fakeAuthorizer struct{ allow bool }

func (f fakeAuthorizer) Check(context.Context, string, string, string) (bool, error) {
	return f.allow, nil
}
func (fakeAuthorizer) WriteGrant(context.Context, string, string, string) error  { return nil }
func (fakeAuthorizer) DeleteGrant(context.Context, string, string, string) error { return nil }

func newExplainService(t *testing.T) (*AuthService, *explainStore, *common.User, *common.User) {
	t.Helper()
	mem := newMemStore()
	store := &explainStore{memStore: mem, machine: &common.Machine{ID: "m1", Name: "web-01"}}
	svc := NewAuthService(store, common.NewLogger(common.ERROR))
	alice := addUser(mem, "alice", common.RoleUser, false, "")
	admin := addUser(mem, "admin", common.RoleAdmin, true, "")
	return svc, store, alice, admin
}

func TestExplainAccessGrantStates(t *testing.T) {
	svc, store, alice, admin := newExplainService(t)
	past := time.Now().Add(-time.Hour)
	future := time.Now().Add(time.Hour)
	reviewed := time.Now()
	store.grants = []*common.Permission{
		{ID: "p-expired", UserID: alice.ID, MachineID: "m1", AccessType: "ssh", RemoteUsers: []string{"root"}, ExpiresAt: &past},
		{ID: "p-scp", UserID: alice.ID, MachineID: "m1", AccessType: "scp", RemoteUsers: []string{"root"}},
		{ID: "p-deploy", UserID: alice.ID, MachineID: "m1", AccessType: "both", RemoteUsers: []string{"deploy"},
			GrantedBy: admin.ID, GrantedAt: reviewed, ExpiresAt: &future},
	}
	store.requests = []*common.AccessRequest{
		{ID: "r-ok", MachineID: "m1", Status: "approved", Reason: "release", ReviewedAt: &reviewed, ReviewedBy: &admin.ID},
		{ID: "r-rej", MachineID: "m1", Status: "rejected", RemoteUsers: []string{"root"}},
		{ID: "r-exp", MachineID: "m1", Status: "expired", RemoteUsers: []string{"app"}},
		{ID: "r-other", MachineID: "m2", Status: "pending"},
	}

	store.hasRemote = false
	ex, err := svc.ExplainAccess(context.Background(), alice.ID, "m1", "ssh", "root")
	if err != nil {
		t.Fatal(err)
	}
	details := map[string]string{}
	for _, l := range ex.Chain {
		details[l.Ref] = l.Detail
	}
	for ref, want := range map[string]string{
		"permission:p-expired": "grant expired",
		"permission:p-scp":     "grant is for scp, not ssh",
		"permission:p-deploy":  "grant allows deploy, not root",
		"access_request:r-rej": "was rejected",
		"access_request:r-exp": "is expired (it does not cover root)",
	} {
		if !strings.Contains(details[ref], want) {
			t.Errorf("%s detail = %q, want %q", ref, details[ref], want)
		}
	}
	if _, ok := details["access_request:r-other"]; ok {
		t.Error("a request for another machine was listed")
	}
	if ex.Allowed || !strings.Contains(ex.Summary, "grant expired") {
		t.Errorf("summary = %q", ex.Summary)
	}

	store.hasRemote = true
	ex, _ = svc.ExplainAccess(context.Background(), alice.ID, "m1", "ssh", "deploy")
	if !ex.Allowed || !strings.Contains(ex.Summary, "Allowed") {
		t.Fatalf("explanation = %+v", ex)
	}
	for _, l := range ex.Chain {
		if l.Ref == "permission:p-deploy" && (!l.Holds || l.Via != "access_request:r-ok" || l.Reason != "release" || l.GrantedBy != "admin") {
			t.Errorf("deploy grant link = %+v", l)
		}
	}
}

func TestExplainAccessRoleOpenFGAAndNothing(t *testing.T) {
	svc, store, alice, admin := newExplainService(t)

	ex, _ := svc.ExplainAccess(context.Background(), admin.ID, "m1", "ssh", "root")
	if !ex.Allowed || ex.Chain[0].Ref != "role:admin" || !ex.Chain[0].Holds {
		t.Errorf("admin explanation = %+v", ex)
	}

	ex, _ = svc.ExplainAccess(context.Background(), alice.ID, "m1", "ssh", "root")
	if ex.Allowed || ex.Chain[0].Relation != common.LinkNone || !strings.Contains(ex.Summary, "osh -r root@web-01") {
		t.Errorf("no-relationship explanation = %+v", ex)
	}

	// OpenFGA refuses although a local grant would allow it.
	store.grants = []*common.Permission{{ID: "p1", UserID: alice.ID, MachineID: "m1", AccessType: "ssh", RemoteUsers: []string{"root"}}}
	svc.SetAuthorizer(fakeAuthorizer{allow: false})
	ex, _ = svc.ExplainAccess(context.Background(), alice.ID, "m1", "ssh", "root")
	if ex.Allowed || !strings.Contains(ex.Summary, "OpenFGA refuses") {
		t.Errorf("OpenFGA denial = %+v", ex)
	}
	if last := ex.Chain[len(ex.Chain)-1]; last.Relation != common.LinkOpenFGA {
		t.Errorf("chain does not mention OpenFGA: %+v", ex.Chain)
	}

	store.grants = nil
	store.requests = []*common.AccessRequest{{ID: "r1", MachineID: "m1", Status: "pending", RemoteUsers: []string{"root"}}}
	svc.SetAuthorizer(nil)
	ex, _ = svc.ExplainAccess(context.Background(), alice.ID, "m1", "ssh", "root")
	if !strings.Contains(ex.Summary, "waiting for approval (access_request:r1)") {
		t.Errorf("pending summary = %q", ex.Summary)
	}
}

func TestExplainAccessErrors(t *testing.T) {
	svc, store, alice, _ := newExplainService(t)
	if _, err := svc.ExplainAccess(context.Background(), "ghost", "m1", "ssh", "root"); !errors.Is(err, database.ErrNotFound) {
		t.Errorf("unknown user: %v", err)
	}
	if _, err := svc.ExplainAccess(context.Background(), alice.ID, "nope", "ssh", "root"); !errors.Is(err, database.ErrNotFound) {
		t.Errorf("unknown machine: %v", err)
	}
	for _, method := range []string{"ListPairPermissions", "ListUserAccessRequests"} {
		store.fail = map[string]error{method: errDB}
		if _, err := svc.ExplainAccess(context.Background(), alice.ID, "m1", "ssh", "root"); !errors.Is(err, errDB) {
			t.Errorf("%s failure: %v", method, err)
		}
	}
}

func TestExplainUsernameFallsBackToID(t *testing.T) {
	svc, _, _, _ := newExplainService(t)
	if got := svc.username(context.Background(), "gone"); got != "gone" {
		t.Errorf("username = %q", got)
	}
	if got := svc.username(context.Background(), ""); got != "" {
		t.Errorf("username(\"\") = %q", got)
	}
}
