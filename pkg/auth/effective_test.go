package auth

import (
	"context"
	"errors"
	"io"
	"testing"
	"time"

	"github.com/orion-belt-dev/orion-belt/pkg/common"
	"github.com/orion-belt-dev/orion-belt/pkg/database"
)

// effectiveStore serves the users, machines and grants EffectiveFor* read.
type effectiveStore struct {
	database.Store
	users    []*common.User
	machines []*common.Machine
	perms    []*common.Permission
	fail     error
}

func (s *effectiveStore) GetUser(_ context.Context, id string) (*common.User, error) {
	for _, u := range s.users {
		if u.ID == id {
			return u, nil
		}
	}
	return nil, database.ErrNotFound
}

func (s *effectiveStore) GetMachine(_ context.Context, id string) (*common.Machine, error) {
	for _, m := range s.machines {
		if m.ID == id {
			return m, nil
		}
	}
	return nil, database.ErrNotFound
}

func (s *effectiveStore) ListUsers(context.Context, int, int) ([]*common.User, error) {
	return s.users, s.fail
}

func (s *effectiveStore) ListMachines(context.Context, int, int) ([]*common.Machine, error) {
	return s.machines, s.fail
}

func (s *effectiveStore) ListUserPermissions(_ context.Context, id string) ([]*common.Permission, error) {
	var out []*common.Permission
	for _, p := range s.perms {
		if p.UserID == id {
			out = append(out, p)
		}
	}
	return out, nil
}

func (s *effectiveStore) ListMachinePermissions(_ context.Context, id string) ([]*common.Permission, error) {
	var out []*common.Permission
	for _, p := range s.perms {
		if p.MachineID == id {
			out = append(out, p)
		}
	}
	return out, nil
}

func newEffectiveFixture() (*AuthService, *effectiveStore, map[string]string) {
	admin := common.NewUser("root", "r@x", "", true)
	op := common.NewUser("oncall", "o@x", "", false)
	op.Role = common.RoleOperator
	alice := common.NewUser("alice", "a@x", "", false)
	web := common.NewMachine("web-01", "web", 22, nil)
	db := common.NewMachine("db-01", "db", 22, nil)
	later := time.Now().Add(time.Hour)
	st := &effectiveStore{
		users:    []*common.User{admin, op, alice},
		machines: []*common.Machine{web, db},
		perms: []*common.Permission{
			common.NewPermission(alice.ID, web.ID, "both", []string{"deploy"}, admin.ID, &later),
			common.NewPermission(alice.ID, db.ID, "rdp", nil, admin.ID, nil),
			common.NewPermission(alice.ID, db.ID, "k8s", []string{"payments/edit"}, admin.ID, nil),
		},
	}
	ids := map[string]string{"admin": admin.ID, "op": op.ID, "alice": alice.ID, "web": web.ID, "db": db.ID}
	return NewAuthService(st, common.NewLoggerTo(common.FATAL, io.Discard)), st, ids
}

func TestEffectiveForUser(t *testing.T) {
	svc, _, ids := newEffectiveFixture()
	ctx := context.Background()

	rows, err := svc.EffectiveForUser(ctx, ids["alice"])
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]EffectiveAccess{}
	for _, r := range rows {
		got[r.MachineName+"/"+r.AccessType] = r
	}
	// "both" is ssh and scp; rdp has no remote user; k8s keeps its scope.
	if len(rows) != 4 || got["web-01/ssh"].RemoteUsers[0] != "deploy" || got["web-01/scp"].ExpiresAt == nil {
		t.Fatalf("alice = %+v", rows)
	}
	if r := got["db-01/rdp"]; r.RemoteUsers == nil || len(r.RemoteUsers) != 0 {
		t.Errorf("rdp remote users = %#v, want empty", r.RemoteUsers)
	}
	if got["db-01/k8s"].RemoteUsers[0] != "payments/edit" || got["db-01/k8s"].Source[:11] != "permission:" {
		t.Errorf("k8s row = %+v", got["db-01/k8s"])
	}

	rows, _ = svc.EffectiveForUser(ctx, ids["admin"])
	if len(rows) != 2 || rows[0].AccessType != "*" || rows[0].Source != "role:admin" || rows[0].RemoteUsers != nil {
		t.Errorf("admin = %+v, want every machine by role", rows)
	}
	if _, err := svc.EffectiveForUser(ctx, "ghost"); !errors.Is(err, database.ErrNotFound) {
		t.Errorf("unknown user = %v", err)
	}
}

func TestEffectiveForMachine(t *testing.T) {
	svc, st, ids := newEffectiveFixture()
	ctx := context.Background()

	rows, err := svc.EffectiveForMachine(ctx, ids["web"])
	if err != nil {
		t.Fatal(err)
	}
	sources := map[string]string{}
	for _, r := range rows {
		sources[r.Username+"/"+r.AccessType] = r.Source
	}
	if sources["root/*"] != "role:admin" || sources["oncall/*"] != "role:operator" ||
		sources["alice/ssh"] == "" || sources["alice/scp"] == "" || len(rows) != 4 {
		t.Errorf("web-01 = %v", sources)
	}
	// Sorted by user, then type.
	for i := 1; i < len(rows); i++ {
		if rows[i-1].Username > rows[i].Username {
			t.Errorf("not sorted: %s before %s", rows[i-1].Username, rows[i].Username)
		}
	}
	if _, err := svc.EffectiveForMachine(ctx, "ghost"); !errors.Is(err, database.ErrNotFound) {
		t.Errorf("unknown machine = %v", err)
	}

	st.fail = errors.New("db down")
	if _, err := svc.EffectiveForMachine(ctx, ids["web"]); err == nil {
		t.Error("ListUsers failure hidden")
	}
	if _, err := svc.EffectiveForUser(ctx, ids["alice"]); err == nil {
		t.Error("ListMachines failure hidden")
	}
}
