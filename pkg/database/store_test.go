package database

import (
	"context"
	"errors"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/orion-belt-dev/orion-belt/pkg/common"
	"github.com/orion-belt-dev/orion-belt/pkg/notify"
)

// fixture is a migrated, empty database with two users and one machine.
type fixture struct {
	pg      *PostgresStore
	ctx     context.Context
	admin   *common.User
	alice   *common.User
	machine *common.Machine
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	f := &fixture{pg: openTestPostgres(t), ctx: context.Background()}
	f.admin = f.user(t, "admin", true)
	f.alice = f.user(t, "alice", false)
	f.machine = f.machineNamed(t, "web-01")
	return f
}

func (f *fixture) user(t *testing.T, name string, admin bool) *common.User {
	t.Helper()
	u := common.NewUser(name, name+"@example.com", "ssh-ed25519 AAAA"+name, admin)
	if err := f.pg.CreateUser(f.ctx, u); err != nil {
		t.Fatalf("CreateUser(%s): %v", name, err)
	}
	return u
}

func (f *fixture) machineNamed(t *testing.T, name string) *common.Machine {
	t.Helper()
	m := common.NewMachine(name, name+".internal", 22, map[string]string{"env": "prod"})
	if err := f.pg.CreateMachine(f.ctx, m); err != nil {
		t.Fatalf("CreateMachine(%s): %v", name, err)
	}
	return m
}

// roughly compares a timestamp read back from a TIMESTAMP (without time zone)
// column with the value written. The column keeps the writer's local wall
// clock and drops the zone, and lib/pq returns it labelled UTC, so the
// comparison is on wall-clock fields, to the microsecond the column stores.
func roughly(stored, written time.Time) bool {
	w := written.In(time.Local)
	wall := time.Date(w.Year(), w.Month(), w.Day(), w.Hour(), w.Minute(), w.Second(), w.Nanosecond(), time.UTC)
	d := stored.Sub(wall)
	return d > -time.Millisecond && d < time.Millisecond
}

func ptr[T any](v T) *T { return &v }

// --- lifecycle and registry --------------------------------------------------------

func TestNewStore_Registry(t *testing.T) {
	if _, err := NewStore("mysql", "dsn"); !errors.Is(err, ErrUnsupportedDriver) {
		t.Errorf("NewStore(mysql) = %v, want ErrUnsupportedDriver", err)
	}
	s, err := NewStore("postgres", "postgres://user@127.0.0.1:1/db?sslmode=disable&connect_timeout=1")
	if err != nil {
		t.Fatalf("NewStore(postgres) = %v; sql.Open is lazy and must not dial", err)
	}
	if _, ok := s.(*PostgresStore); !ok {
		t.Errorf("NewStore(postgres) returned %T", s)
	}
	if err := s.Connect(context.Background()); err == nil {
		t.Error("Connect to a closed port succeeded")
	}
	_ = s.Close()
}

func TestConnectPingAndMigrateAreIdempotent(t *testing.T) {
	f := newFixture(t)
	if err := f.pg.Connect(f.ctx); err != nil {
		t.Fatalf("Connect: %v", err)
	}
	if err := f.pg.Ping(f.ctx); err != nil {
		t.Fatalf("Ping: %v", err)
	}
	for i := 0; i < 2; i++ {
		if err := f.pg.Migrate(f.ctx); err != nil {
			t.Fatalf("Migrate #%d on a migrated database: %v", i, err)
		}
	}
}

// --- users ---------------------------------------------------------------------------

func TestUsers_CRUD(t *testing.T) {
	f := newFixture(t)

	got, err := f.pg.GetUser(f.ctx, f.alice.ID)
	if err != nil {
		t.Fatalf("GetUser: %v", err)
	}
	if got.Username != "alice" || got.Role != common.RoleUser || got.IsAdmin || !roughly(got.CreatedAt, f.alice.CreatedAt) {
		t.Errorf("GetUser = %+v", got)
	}
	byName, err := f.pg.GetUserByUsername(f.ctx, "admin")
	if err != nil || byName.ID != f.admin.ID || byName.Role != common.RoleAdmin {
		t.Errorf("GetUserByUsername(admin) = %+v, %v", byName, err)
	}

	got.Email = "new@example.com"
	got.Role = common.RoleOperator
	got.MFAEnabled = true
	got.WebAuthnEnabled = true
	got.TOTPSecret = "SECRET"
	got.BackupCodesHash = "hashes"
	got.PasswordHash = "$argon2id$x"
	if err := f.pg.UpdateUser(f.ctx, got); err != nil {
		t.Fatalf("UpdateUser: %v", err)
	}
	again, _ := f.pg.GetUser(f.ctx, f.alice.ID)
	if again.Email != "new@example.com" || again.Role != common.RoleOperator || !again.MFAEnabled ||
		!again.WebAuthnEnabled || again.TOTPSecret != "SECRET" || again.BackupCodesHash != "hashes" || again.PasswordHash != "$argon2id$x" {
		t.Errorf("after UpdateUser = %+v", again)
	}

	users, err := f.pg.ListUsers(f.ctx, 10, 0)
	if err != nil || len(users) != 2 {
		t.Fatalf("ListUsers = %d, %v", len(users), err)
	}
	if page, _ := f.pg.ListUsers(f.ctx, 1, 1); len(page) != 1 {
		t.Errorf("ListUsers(limit 1, offset 1) = %d rows", len(page))
	}

	if err := f.pg.DeleteUser(f.ctx, f.alice.ID); err != nil {
		t.Fatalf("DeleteUser: %v", err)
	}
	if _, err := f.pg.GetUser(f.ctx, f.alice.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("GetUser after delete = %v, want ErrNotFound", err)
	}
}

func TestUsers_NotFoundAndDuplicates(t *testing.T) {
	f := newFixture(t)
	if _, err := f.pg.GetUserByUsername(f.ctx, "ghost"); !errors.Is(err, ErrNotFound) {
		t.Errorf("GetUserByUsername(ghost) = %v", err)
	}
	if err := f.pg.UpdateUser(f.ctx, &common.User{ID: "ghost"}); !errors.Is(err, ErrNotFound) {
		t.Errorf("UpdateUser(ghost) = %v", err)
	}
	if err := f.pg.DeleteUser(f.ctx, "ghost"); !errors.Is(err, ErrNotFound) {
		t.Errorf("DeleteUser(ghost) = %v", err)
	}
	dup := common.NewUser("alice", "x@example.com", "", false)
	if err := f.pg.CreateUser(f.ctx, dup); err == nil {
		t.Error("CreateUser with a duplicate username succeeded")
	}
	if err := f.pg.CreateFirstUser(f.ctx, dup); !errors.Is(err, ErrAlreadyInitialized) {
		t.Errorf("CreateFirstUser with users present = %v", err)
	}
}

func TestCreateUser_DefaultsRoleFromAdminFlag(t *testing.T) {
	f := newFixture(t)
	for _, admin := range []bool{true, false} {
		u := common.NewUser("legacy", "l@example.com", "", admin)
		u.ID = "legacy-" + map[bool]string{true: "a", false: "u"}[admin]
		u.Username = u.ID
		u.Role = ""
		if err := f.pg.CreateUser(f.ctx, u); err != nil {
			t.Fatal(err)
		}
		got, _ := f.pg.GetUser(f.ctx, u.ID)
		want := map[bool]string{true: common.RoleAdmin, false: common.RoleUser}[admin]
		if got.Role != want {
			t.Errorf("role for is_admin=%v = %q, want %q", admin, got.Role, want)
		}
	}
}

func TestCreateFirstUser_EmptyDatabaseAndInsertFailure(t *testing.T) {
	pg := openTestPostgres(t)
	ctx := context.Background()
	first := common.NewUser("root", "r@example.com", "", true)

	bad := *first
	bad.Username = string(make([]byte, 300)) // longer than VARCHAR(255)
	if err := pg.CreateFirstUser(ctx, &bad); err == nil {
		t.Error("CreateFirstUser with an oversized username succeeded")
	}
	if err := pg.CreateFirstUser(ctx, first); err != nil {
		t.Fatalf("CreateFirstUser on an empty database: %v", err)
	}
	if got, err := pg.GetUser(ctx, first.ID); err != nil || got.Role != common.RoleAdmin {
		t.Errorf("first user = %+v, %v", got, err)
	}
}

// DeleteUser removes the user's own rows and detaches rows that only name
// them as an actor, so other principals' history survives.
func TestDeleteUser_CascadesOwnRowsAndDetachesAttribution(t *testing.T) {
	f := newFixture(t)
	bob := f.user(t, "bob", false)

	// alice's own rows: must go.
	sess := common.NewSession(f.alice.ID, f.machine.ID, "root", "/rec")
	req := common.NewAccessRequest(f.alice.ID, f.machine.ID, []string{"root"}, "x", 60)
	own := common.NewPermission(f.alice.ID, f.machine.ID, "ssh", []string{"root"}, f.admin.ID, nil)
	// bob's rows that alice merely reviewed/granted: must stay.
	bobReq := common.NewAccessRequest(bob.ID, f.machine.ID, []string{"root"}, "y", 60)
	bobReq.ReviewedBy = &f.alice.ID
	bobPerm := common.NewPermission(bob.ID, f.machine.ID, "ssh", []string{"root"}, f.alice.ID, nil)
	for _, err := range []error{
		f.pg.CreateSession(f.ctx, sess),
		f.pg.CreateAccessRequest(f.ctx, req),
		f.pg.CreatePermission(f.ctx, own),
		f.pg.CreateAccessRequest(f.ctx, bobReq),
		f.pg.CreatePermission(f.ctx, bobPerm),
		f.pg.CreateAuditLog(f.ctx, common.NewAuditLog(f.alice.ID, "login", "session", "1.2.3.4", nil)),
	} {
		if err != nil {
			t.Fatal(err)
		}
	}
	cert := common.NewSSHCertificate("99", common.CATypeUser, bob.ID, "bob", []string{"bob"}, "fp", time.Now(), time.Now().Add(time.Hour))
	if err := f.pg.CreateSSHCertificate(f.ctx, cert); err != nil {
		t.Fatal(err)
	}
	if err := f.pg.RevokeSSHCertificate(f.ctx, "99", f.alice.ID, "lost laptop"); err != nil {
		t.Fatal(err)
	}

	if err := f.pg.DeleteUser(f.ctx, f.alice.ID); err != nil {
		t.Fatalf("DeleteUser: %v", err)
	}

	if _, err := f.pg.GetSession(f.ctx, sess.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("alice's session survived: %v", err)
	}
	if _, err := f.pg.GetAccessRequest(f.ctx, req.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("alice's request survived: %v", err)
	}
	if _, err := f.pg.GetPermission(f.ctx, own.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("alice's permission survived: %v", err)
	}
	if got, err := f.pg.GetAccessRequest(f.ctx, bobReq.ID); err != nil || got.ReviewedBy != nil {
		t.Errorf("bob's request = %+v, %v; want kept with reviewer cleared", got, err)
	}
	if got, err := f.pg.GetPermission(f.ctx, bobPerm.ID); err != nil || got.GrantedBy != "" {
		t.Errorf("bob's permission = %+v, %v; want kept with grantor cleared", got, err)
	}
	if got, err := f.pg.GetSSHCertificateBySerial(f.ctx, "99"); err != nil || got.RevokedBy != nil || got.RevokedAt == nil {
		t.Errorf("bob's cert = %+v, %v; want still revoked, revoker cleared", got, err)
	}
	if logs, err := f.pg.ListAuditLogs(f.ctx, 10, 0, nil); err != nil || len(logs) != 1 || logs[0].UserID != "" {
		t.Errorf("audit logs = %+v, %v; want kept with user cleared", logs, err)
	}
}

// Before the repair migration, granted_by was NOT NULL. DeleteUser's
// fallback UPDATE runs in a transaction Postgres has already aborted, so on
// such a schema the delete is refused rather than half-applied.
func TestDeleteUser_PreMigrationGrantedByNotNull(t *testing.T) {
	f := newFixture(t)
	bob := f.user(t, "bob", false)
	if err := f.pg.CreatePermission(f.ctx, common.NewPermission(bob.ID, f.machine.ID, "ssh", []string{"root"}, f.alice.ID, nil)); err != nil {
		t.Fatal(err)
	}
	if _, err := f.pg.db.ExecContext(f.ctx, `ALTER TABLE permissions ALTER COLUMN granted_by SET NOT NULL`); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = f.pg.db.ExecContext(f.ctx, `ALTER TABLE permissions ALTER COLUMN granted_by DROP NOT NULL`)
	})

	err := f.pg.DeleteUser(f.ctx, f.alice.ID)
	if err == nil || !strings.Contains(err.Error(), "failed to clear permission grantor") {
		t.Fatalf("DeleteUser = %v, want the grantor error", err)
	}
	if _, err := f.pg.GetUser(f.ctx, f.alice.ID); err != nil {
		t.Errorf("alice was deleted despite the failure: %v", err)
	}
}

// --- machines --------------------------------------------------------------------------

func TestMachines_CRUD(t *testing.T) {
	f := newFixture(t)

	got, err := f.pg.GetMachine(f.ctx, f.machine.ID)
	if err != nil || got.Name != "web-01" || got.Tags["env"] != "prod" || got.Port != 22 || got.IsActive {
		t.Fatalf("GetMachine = %+v, %v", got, err)
	}
	if byName, err := f.pg.GetMachineByName(f.ctx, "web-01"); err != nil || byName.ID != f.machine.ID {
		t.Errorf("GetMachineByName = %+v, %v", byName, err)
	}

	now := time.Now()
	got.IsActive = true
	got.LastSeenAt = &now
	got.AgentID = "agent-1"
	got.Tags = map[string]string{"env": "staging"}
	if err := f.pg.UpdateMachine(f.ctx, got); err != nil {
		t.Fatalf("UpdateMachine: %v", err)
	}
	f.machineNamed(t, "db-01") // inactive

	active, err := f.pg.ListActiveMachines(f.ctx)
	if err != nil || len(active) != 1 || active[0].AgentID != "agent-1" || active[0].Tags["env"] != "staging" || active[0].LastSeenAt == nil {
		t.Errorf("ListActiveMachines = %+v, %v", active, err)
	}
	if all, err := f.pg.ListMachines(f.ctx, 10, 0); err != nil || len(all) != 2 {
		t.Errorf("ListMachines = %d, %v", len(all), err)
	}

	if _, err := f.pg.GetMachine(f.ctx, "ghost"); !errors.Is(err, ErrNotFound) {
		t.Errorf("GetMachine(ghost) = %v", err)
	}
	if _, err := f.pg.GetMachineByName(f.ctx, "ghost"); !errors.Is(err, ErrNotFound) {
		t.Errorf("GetMachineByName(ghost) = %v", err)
	}
	if err := f.pg.UpdateMachine(f.ctx, &common.Machine{ID: "ghost"}); !errors.Is(err, ErrNotFound) {
		t.Errorf("UpdateMachine(ghost) = %v", err)
	}
	if err := f.pg.CreateMachine(f.ctx, common.NewMachine("web-01", "dup", 22, nil)); err == nil {
		t.Error("CreateMachine with a duplicate name succeeded")
	}
}

func TestDeleteMachine_RemovesDependentsAndListsRecordings(t *testing.T) {
	f := newFixture(t)
	s1 := common.NewSession(f.alice.ID, f.machine.ID, "root", "/rec")
	s2 := common.NewSession(f.alice.ID, f.machine.ID, "root", "/rec")
	s2.RecordingPath = ""
	for _, err := range []error{
		f.pg.CreateSession(f.ctx, s1),
		f.pg.CreateSession(f.ctx, s2),
		f.pg.CreatePermission(f.ctx, common.NewPermission(f.alice.ID, f.machine.ID, "ssh", []string{"root"}, f.admin.ID, nil)),
		f.pg.CreateAccessRequest(f.ctx, common.NewAccessRequest(f.alice.ID, f.machine.ID, []string{"root"}, "r", 60)),
	} {
		if err != nil {
			t.Fatal(err)
		}
	}

	paths, err := f.pg.ListSessionRecordingPathsForMachine(f.ctx, f.machine.ID)
	if err != nil || len(paths) != 1 || paths[0] != s1.RecordingPath {
		t.Errorf("recording paths = %v, %v; want only the non-empty one", paths, err)
	}

	if err := f.pg.DeleteMachine(f.ctx, f.machine.ID); err != nil {
		t.Fatalf("DeleteMachine: %v", err)
	}
	if perms, _ := f.pg.ListUserPermissions(f.ctx, f.alice.ID); len(perms) != 0 {
		t.Errorf("permissions survived machine delete: %v", perms)
	}
	if reqs, _ := f.pg.ListUserAccessRequests(f.ctx, f.alice.ID, 10, 0); len(reqs) != 0 {
		t.Errorf("access requests survived machine delete: %v", reqs)
	}
	if sessions, _ := f.pg.ListUserSessions(f.ctx, f.alice.ID, 10, 0); len(sessions) != 0 {
		t.Errorf("sessions survived machine delete: %v", sessions)
	}
	if err := f.pg.DeleteMachine(f.ctx, f.machine.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("second DeleteMachine = %v, want ErrNotFound", err)
	}
}

// --- sessions ------------------------------------------------------------------------------

func TestSessions_Lifecycle(t *testing.T) {
	f := newFixture(t)
	web := common.NewSessionWithSource(f.alice.ID, f.machine.ID, "deploy", "/rec", "web")
	ssh := common.NewSession(f.admin.ID, f.machine.ID, "root", "/rec")
	ssh.Source = "" // stored as the ssh default
	for _, s := range []*common.Session{web, ssh} {
		if err := f.pg.CreateSession(f.ctx, s); err != nil {
			t.Fatal(err)
		}
	}

	got, err := f.pg.GetSession(f.ctx, web.ID)
	if err != nil || got.Source != "web" || got.RemoteUser != "deploy" || got.Status != "active" || got.EndTime != nil {
		t.Fatalf("GetSession = %+v, %v", got, err)
	}
	if got, _ := f.pg.GetSession(f.ctx, ssh.ID); got.Source != "ssh" {
		t.Errorf("blank source stored as %q, want ssh", got.Source)
	}

	active, err := f.pg.ListActiveSessions(f.ctx)
	if err != nil || len(active) != 2 {
		t.Errorf("ListActiveSessions = %d, %v", len(active), err)
	}

	end := time.Now()
	if err := f.pg.EndSession(f.ctx, web.ID, end); err != nil {
		t.Fatalf("EndSession: %v", err)
	}
	if got, _ := f.pg.GetSession(f.ctx, web.ID); got.Status != "completed" || got.EndTime == nil || !roughly(*got.EndTime, end) {
		t.Errorf("ended session = %+v", got)
	}

	ssh.Status = "terminated"
	ssh.EndTime = &end
	if err := f.pg.UpdateSession(f.ctx, ssh); err != nil {
		t.Fatalf("UpdateSession: %v", err)
	}
	if active, _ := f.pg.ListActiveSessions(f.ctx); len(active) != 0 {
		t.Errorf("active sessions after ending both = %d", len(active))
	}

	all, err := f.pg.ListSessions(f.ctx, 0, 0) // limit 0 uses the default page
	if err != nil || len(all) != 2 {
		t.Errorf("ListSessions = %d, %v", len(all), err)
	}
	mine, err := f.pg.ListUserSessions(f.ctx, f.alice.ID, 10, 0)
	if err != nil || len(mine) != 1 || mine[0].ID != web.ID {
		t.Errorf("ListUserSessions = %+v, %v", mine, err)
	}

	if _, err := f.pg.GetSession(f.ctx, "ghost"); !errors.Is(err, ErrNotFound) {
		t.Errorf("GetSession(ghost) = %v", err)
	}
	if err := f.pg.UpdateSession(f.ctx, &common.Session{ID: "ghost"}); !errors.Is(err, ErrNotFound) {
		t.Errorf("UpdateSession(ghost) = %v", err)
	}
	if err := f.pg.EndSession(f.ctx, "ghost", end); !errors.Is(err, ErrNotFound) {
		t.Errorf("EndSession(ghost) = %v", err)
	}
	if err := f.pg.CreateSession(f.ctx, common.NewSession("ghost-user", f.machine.ID, "root", "/rec")); err == nil {
		t.Error("CreateSession for an unknown user succeeded")
	}
}

func TestSessionDetailsAndMachineNames(t *testing.T) {
	f := newFixture(t)
	s := common.NewSession(f.alice.ID, f.machine.ID, "root", "/rec")
	if err := f.pg.CreateSession(f.ctx, s); err != nil {
		t.Fatal(err)
	}

	d, err := f.pg.GetSessionDetails(f.ctx, s.ID)
	if err != nil || d["username"] != "alice" || d["machine_name"] != "web-01" || d["hostname"] != "web-01.internal" || d["status"] != "active" {
		t.Errorf("GetSessionDetails = %v, %v", d, err)
	}
	if _, err := f.pg.GetSessionDetails(f.ctx, "ghost"); !errors.Is(err, ErrNotFound) {
		t.Errorf("GetSessionDetails(ghost) = %v", err)
	}

	rows, err := f.pg.ListUserSessionsWithMachineNames(f.ctx, f.alice.ID, 10, 0)
	if err != nil || len(rows) != 1 || rows[0]["machine_name"] != "web-01" || rows[0]["id"] != s.ID {
		t.Errorf("ListUserSessionsWithMachineNames = %v, %v", rows, err)
	}
}

// --- access requests ---------------------------------------------------------------------------

func TestAccessRequests_Lifecycle(t *testing.T) {
	f := newFixture(t)
	old := common.NewAccessRequest(f.alice.ID, f.machine.ID, []string{"root", "deploy"}, "old", 600)
	old.RequestedAt = time.Now().Add(-10 * 24 * time.Hour)
	fresh := common.NewAccessRequest(f.alice.ID, f.machine.ID, []string{"deploy"}, "fresh", 600)
	fresh.AccessType = ""
	for _, r := range []*common.AccessRequest{old, fresh} {
		if err := f.pg.CreateAccessRequest(f.ctx, r); err != nil {
			t.Fatal(err)
		}
	}

	got, err := f.pg.GetAccessRequest(f.ctx, old.ID)
	if err != nil || len(got.RemoteUsers) != 2 || got.RemoteUsers[1] != "deploy" || got.Status != "pending" || got.AccessType != "both" {
		t.Fatalf("GetAccessRequest = %+v, %v", got, err)
	}
	if fresh.AccessType != "both" {
		t.Errorf("blank access type = %q, want both", fresh.AccessType)
	}

	pending, err := f.pg.ListPendingAccessRequests(f.ctx)
	if err != nil || len(pending) != 2 || pending[0].ID != fresh.ID {
		t.Errorf("ListPendingAccessRequests = %+v, %v; want newest first", pending, err)
	}

	n, err := f.pg.ExpireStalePendingAccessRequests(f.ctx, 7*24*time.Hour)
	if err != nil || n != 1 {
		t.Fatalf("ExpireStalePendingAccessRequests = %d, %v; want the 10-day-old one", n, err)
	}
	if n, _ := f.pg.ExpireStalePendingAccessRequests(f.ctx, 0); n != 0 {
		t.Errorf("default window expired %d more", n)
	}
	if got, _ := f.pg.GetAccessRequest(f.ctx, old.ID); got.Status != "expired" {
		t.Errorf("old request status = %q", got.Status)
	}

	now := time.Now()
	fresh.Status = "approved"
	fresh.ReviewedAt = &now
	fresh.ReviewedBy = &f.admin.ID
	fresh.ExpiresAt = ptr(now.Add(time.Hour))
	fresh.Duration = 3600
	if err := f.pg.UpdateAccessRequest(f.ctx, fresh); err != nil {
		t.Fatalf("UpdateAccessRequest: %v", err)
	}
	got, _ = f.pg.GetAccessRequest(f.ctx, fresh.ID)
	if got.Status != "approved" || got.ReviewedBy == nil || *got.ReviewedBy != f.admin.ID || got.Duration != 3600 || got.ExpiresAt == nil {
		t.Errorf("updated request = %+v", got)
	}

	if all, err := f.pg.ListAllAccessRequests(f.ctx, 10, 0); err != nil || len(all) != 2 {
		t.Errorf("ListAllAccessRequests = %d, %v", len(all), err)
	}
	if mine, err := f.pg.ListUserAccessRequests(f.ctx, f.alice.ID, 1, 0); err != nil || len(mine) != 1 {
		t.Errorf("ListUserAccessRequests(limit 1) = %d, %v", len(mine), err)
	}
	if _, err := f.pg.GetAccessRequest(f.ctx, "ghost"); !errors.Is(err, ErrNotFound) {
		t.Errorf("GetAccessRequest(ghost) = %v", err)
	}
	if err := f.pg.UpdateAccessRequest(f.ctx, &common.AccessRequest{ID: "ghost"}); !errors.Is(err, ErrNotFound) {
		t.Errorf("UpdateAccessRequest(ghost) = %v", err)
	}
	if err := f.pg.CreateAccessRequest(f.ctx, common.NewAccessRequest("ghost", f.machine.ID, nil, "", 0)); err == nil {
		t.Error("CreateAccessRequest for an unknown user succeeded")
	}
}

// --- permissions -----------------------------------------------------------------------------

func TestPermissions_ChecksAndExpiry(t *testing.T) {
	f := newFixture(t)
	db := f.machineNamed(t, "db-01")
	past := time.Now().Add(-time.Hour)

	sshGrant := common.NewPermission(f.alice.ID, f.machine.ID, "ssh", []string{"deploy"}, f.admin.ID, nil)
	bothGrant := common.NewPermission(f.alice.ID, db.ID, "both", []string{"root"}, f.admin.ID, ptr(time.Now().Add(time.Hour)))
	expired := common.NewPermission(f.admin.ID, f.machine.ID, "ssh", []string{"root"}, f.admin.ID, &past)
	for _, p := range []*common.Permission{sshGrant, bothGrant, expired} {
		if err := f.pg.CreatePermission(f.ctx, p); err != nil {
			t.Fatal(err)
		}
	}

	checks := []struct {
		user, machine, access, remote string
		want                          bool
	}{
		{f.alice.ID, f.machine.ID, "ssh", "deploy", true},
		{f.alice.ID, f.machine.ID, "ssh", "root", false},
		{f.alice.ID, f.machine.ID, "scp", "deploy", false},
		{f.alice.ID, db.ID, "scp", "root", true}, // "both" covers any type
		{f.admin.ID, f.machine.ID, "ssh", "root", false},
	}
	for _, c := range checks {
		got, err := f.pg.HasPermissionWithRemoteUser(f.ctx, c.user, c.machine, c.access, c.remote)
		if err != nil || got != c.want {
			t.Errorf("HasPermissionWithRemoteUser(%s,%s,%s) = %v, %v; want %v", c.machine, c.access, c.remote, got, err, c.want)
		}
		any, err := f.pg.HasPermission(f.ctx, c.user, c.machine, c.access)
		wantAny := c.want || (c.user == f.alice.ID && c.machine == f.machine.ID && c.access == "ssh")
		if err != nil || any != wantAny {
			t.Errorf("HasPermission(%s,%s) = %v, %v; want %v", c.machine, c.access, any, err, wantAny)
		}
	}

	if mine, err := f.pg.ListUserPermissions(f.ctx, f.alice.ID); err != nil || len(mine) != 2 {
		t.Errorf("ListUserPermissions = %d, %v", len(mine), err)
	}
	if onMachine, err := f.pg.ListMachinePermissions(f.ctx, f.machine.ID); err != nil || len(onMachine) != 1 {
		t.Errorf("ListMachinePermissions = %d, %v; want the expired grant hidden", len(onMachine), err)
	}
	if all, err := f.pg.ListAllPermissions(f.ctx, 0, 0); err != nil || len(all) != 2 {
		t.Errorf("ListAllPermissions = %d, %v", len(all), err)
	}
	if _, err := f.pg.FindActivePermission(f.ctx, f.admin.ID, f.machine.ID, "ssh"); !errors.Is(err, ErrNotFound) {
		t.Errorf("FindActivePermission(expired) = %v, want ErrNotFound", err)
	}

	sshGrant.RemoteUsers = []string{"deploy", "app"}
	sshGrant.AccessType = "both"
	if err := f.pg.UpdatePermission(f.ctx, sshGrant); err != nil {
		t.Fatalf("UpdatePermission: %v", err)
	}
	if got, _ := f.pg.GetPermission(f.ctx, sshGrant.ID); got.AccessType != "both" || len(got.RemoteUsers) != 2 {
		t.Errorf("updated permission = %+v", got)
	}

	if err := f.pg.DeletePermission(f.ctx, sshGrant.ID); err != nil {
		t.Fatalf("DeletePermission: %v", err)
	}
	if err := f.pg.DeletePermission(f.ctx, sshGrant.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("second DeletePermission = %v", err)
	}
	if _, err := f.pg.GetPermission(f.ctx, sshGrant.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("GetPermission(deleted) = %v", err)
	}
	if err := f.pg.CreatePermission(f.ctx, common.NewPermission("ghost", f.machine.ID, "ssh", nil, "", nil)); err == nil {
		t.Error("CreatePermission for an unknown user succeeded")
	}
}

// --- audit logs ---------------------------------------------------------------------------------

func TestAuditLogs_Filters(t *testing.T) {
	f := newFixture(t)
	entries := []*common.AuditLog{
		common.NewAuditLog(f.alice.ID, "login", "session", "10.0.0.1", map[string]interface{}{"method": "key"}),
		common.NewAuditLog(f.alice.ID, "logout", "session", "10.0.0.1", nil),
		common.NewAuditLog(f.admin.ID, "login", "session", "10.0.0.2", nil),
		common.NewAuditLog(f.admin.ID, "user.create", "user", "10.0.0.2", nil),
	}
	for i, e := range entries {
		e.Timestamp = time.Now().Add(time.Duration(i) * time.Second)
		if err := f.pg.CreateAuditLog(f.ctx, e); err != nil {
			t.Fatal(err)
		}
	}

	cases := []struct {
		name    string
		filters map[string]interface{}
		want    int
	}{
		{"no filter", nil, 4},
		{"by user", map[string]interface{}{"user_id": f.alice.ID}, 2},
		{"by action", map[string]interface{}{"action": "login"}, 2},
		{"by user and action", map[string]interface{}{"user_id": f.admin.ID, "action": "login"}, 1},
		{"by resource", map[string]interface{}{"resource": "user"}, 1},
		{"blank and non-string values ignored", map[string]interface{}{"action": "", "resource": 5, "unknown": "x"}, 4},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			logs, err := f.pg.ListAuditLogs(f.ctx, 10, 0, tc.filters)
			if err != nil || len(logs) != tc.want {
				t.Errorf("ListAuditLogs = %d, %v; want %d", len(logs), err, tc.want)
			}
		})
	}

	logs, _ := f.pg.ListAuditLogs(f.ctx, 1, 0, nil)
	if len(logs) != 1 || logs[0].Action != "user.create" {
		t.Errorf("newest entry = %+v, want user.create", logs)
	}
	first, _ := f.pg.ListAuditLogs(f.ctx, 1, 3, nil)
	if len(first) != 1 || first[0].Metadata["method"] != "key" {
		t.Errorf("oldest entry = %+v, want metadata round-tripped", first)
	}
}

// --- notifications --------------------------------------------------------------------------------

func TestNotifications(t *testing.T) {
	f := newFixture(t)
	n1 := common.NewNotification(f.alice.ID, notify.EventAccessRequestApproved, "Approved", "body", map[string]interface{}{"request_id": "r1"})
	n2 := common.NewNotification(f.alice.ID, notify.EventAccessRequestRejected, "Rejected", "", nil)
	n2.CreatedAt = n1.CreatedAt.Add(time.Second)
	for _, n := range []*common.Notification{n1, n2} {
		if err := f.pg.CreateNotification(f.ctx, n); err != nil {
			t.Fatal(err)
		}
	}

	list, err := f.pg.ListUserNotifications(f.ctx, f.alice.ID, 10, 0)
	if err != nil || len(list) != 2 || list[0].ID != n2.ID || list[1].Metadata["request_id"] != "r1" {
		t.Fatalf("ListUserNotifications = %+v, %v", list, err)
	}
	if c, err := f.pg.CountUnreadNotifications(f.ctx, f.alice.ID); err != nil || c != 2 {
		t.Errorf("CountUnreadNotifications = %d, %v", c, err)
	}

	if err := f.pg.MarkNotificationRead(f.ctx, n1.ID, f.admin.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("marking someone else's notification = %v, want ErrNotFound", err)
	}
	if err := f.pg.MarkNotificationRead(f.ctx, n1.ID, f.alice.ID); err != nil {
		t.Fatalf("MarkNotificationRead: %v", err)
	}
	if err := f.pg.MarkNotificationRead(f.ctx, n1.ID, f.alice.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("marking an already read notification = %v, want ErrNotFound", err)
	}
	if err := f.pg.MarkAllNotificationsRead(f.ctx, f.alice.ID); err != nil {
		t.Fatalf("MarkAllNotificationsRead: %v", err)
	}
	if c, _ := f.pg.CountUnreadNotifications(f.ctx, f.alice.ID); c != 0 {
		t.Errorf("unread after MarkAll = %d", c)
	}

	bad := common.NewNotification(f.alice.ID, "x", "t", "b", map[string]interface{}{"ch": make(chan int)})
	if err := f.pg.CreateNotification(f.ctx, bad); err == nil {
		t.Error("CreateNotification with unencodable metadata succeeded")
	}
	if err := f.pg.CreateNotification(f.ctx, common.NewNotification("ghost", "x", "t", "b", nil)); err == nil {
		t.Error("CreateNotification for an unknown user succeeded")
	}
}

func TestNotificationPrefsAndPolicy(t *testing.T) {
	f := newFixture(t)

	prefs, err := f.pg.GetNotificationPrefs(f.ctx, f.alice.ID)
	if err != nil || !prefs.InAppEnabled || prefs.EmailEnabled {
		t.Fatalf("default prefs = %+v, %v", prefs, err)
	}
	prefs.EmailEnabled = true
	prefs.EventTypes = nil // stored as an empty array, not NULL
	if err := f.pg.UpsertNotificationPrefs(f.ctx, prefs); err != nil {
		t.Fatalf("UpsertNotificationPrefs: %v", err)
	}
	prefs.EventTypes = []string{notify.EventAccessRequestApproved}
	if err := f.pg.UpsertNotificationPrefs(f.ctx, prefs); err != nil {
		t.Fatalf("second UpsertNotificationPrefs: %v", err)
	}
	if got, _ := f.pg.GetNotificationPrefs(f.ctx, f.alice.ID); !got.EmailEnabled || len(got.EventTypes) != 1 {
		t.Errorf("stored prefs = %+v", got)
	}

	policy, err := f.pg.GetNotificationPolicy(f.ctx)
	if err != nil || len(policy.AllowedChannels) != 0 || len(policy.MandatoryEvents) != 0 {
		t.Fatalf("default policy = %+v, %v", policy, err)
	}
	if err := f.pg.UpsertNotificationPolicy(f.ctx, nil); err == nil {
		t.Error("UpsertNotificationPolicy(nil) succeeded")
	}
	policy = &common.NotificationPolicy{
		AllowedChannels: []string{common.ChannelInApp},
		MandatoryEvents: map[string][]string{notify.EventAccessRequestApproved: {common.ChannelEmail}},
	}
	if err := f.pg.UpsertNotificationPolicy(f.ctx, policy); err != nil {
		t.Fatalf("UpsertNotificationPolicy: %v", err)
	}
	got, err := f.pg.GetNotificationPolicy(f.ctx)
	if err != nil {
		t.Fatal(err)
	}
	// Normalize on read promotes the mandatory channel into the allow-list.
	sort.Strings(got.AllowedChannels)
	if len(got.AllowedChannels) != 2 || got.MandatoryEvents[notify.EventAccessRequestApproved][0] != common.ChannelEmail || got.UpdatedAt.IsZero() {
		t.Errorf("stored policy = %+v", got)
	}

	if err := f.pg.UpsertNotificationPolicy(f.ctx, &common.NotificationPolicy{}); err != nil {
		t.Fatalf("UpsertNotificationPolicy(empty): %v", err)
	}
	if got, _ := f.pg.GetNotificationPolicy(f.ctx); got.MandatoryEvents == nil {
		t.Error("policy stored with null mandatory events reads back as a nil map")
	}

	if _, err := f.pg.db.ExecContext(f.ctx, `UPDATE notification_policy SET mandatory_events = '[1]'`); err != nil {
		t.Fatal(err)
	}
	if _, err := f.pg.GetNotificationPolicy(f.ctx); err == nil {
		t.Error("GetNotificationPolicy with a malformed mandatory_events row succeeded")
	}
	if err := f.pg.UpsertNotificationPrefs(f.ctx, &common.NotificationPrefs{UserID: "ghost"}); err == nil {
		t.Error("UpsertNotificationPrefs for an unknown user succeeded")
	}
}

func TestNotificationTemplates(t *testing.T) {
	f := newFixture(t)
	if list, err := f.pg.ListNotificationTemplates(f.ctx); err != nil || len(list) != 0 {
		t.Fatalf("fresh templates = %v, %v", list, err)
	}
	if err := f.pg.UpsertNotificationTemplate(f.ctx, nil); err == nil {
		t.Error("UpsertNotificationTemplate(nil) succeeded")
	}

	rejected := &notify.Template{EventType: notify.EventAccessRequestRejected, Title: "No", Body: "Rejected {{.Reason}}"}
	approved := &notify.Template{EventType: notify.EventAccessRequestApproved, Title: "Yes", Body: "Approved"}
	for _, tmpl := range []*notify.Template{rejected, approved} {
		if err := f.pg.UpsertNotificationTemplate(f.ctx, tmpl); err != nil {
			t.Fatal(err)
		}
		if tmpl.UpdatedAt.IsZero() {
			t.Error("UpsertNotificationTemplate did not set UpdatedAt from the database")
		}
	}
	approved.Title = "Approved!"
	if err := f.pg.UpsertNotificationTemplate(f.ctx, approved); err != nil {
		t.Fatal(err)
	}

	list, err := f.pg.ListNotificationTemplates(f.ctx)
	if err != nil || len(list) != 2 || list[0].EventType != notify.EventAccessRequestApproved || list[0].Title != "Approved!" {
		t.Errorf("templates = %+v, %v; want catalog order with the update applied", list, err)
	}

	if err := f.pg.DeleteNotificationTemplate(f.ctx, notify.EventAccessRequestApproved); err != nil {
		t.Fatal(err)
	}
	if err := f.pg.DeleteNotificationTemplate(f.ctx, "never-stored"); err != nil {
		t.Errorf("deleting an absent template = %v, want nil", err)
	}
	if list, _ := f.pg.ListNotificationTemplates(f.ctx); len(list) != 1 {
		t.Errorf("templates after delete = %d", len(list))
	}
}

// --- API keys, HTTP sessions, SSH keys, WebAuthn --------------------------------------------------

func TestAPIKeys(t *testing.T) {
	f := newFixture(t)
	exp := time.Now().Add(time.Hour)
	k1 := common.NewAPIKey(f.alice.ID, "ci", "hash-1", "ob_1", &exp)
	k2 := common.NewAPIKey(f.alice.ID, "laptop", "hash-2", "ob_2", nil)
	for _, k := range []*common.APIKey{k1, k2} {
		if err := f.pg.CreateAPIKey(f.ctx, k); err != nil {
			t.Fatal(err)
		}
	}
	if err := f.pg.CreateAPIKey(f.ctx, common.NewAPIKey(f.alice.ID, "dup", "hash-1", "ob_x", nil)); err == nil {
		t.Error("CreateAPIKey with a duplicate hash succeeded")
	}

	got, err := f.pg.GetAPIKey(f.ctx, k1.ID)
	if err != nil || got.Name != "ci" || got.ExpiresAt == nil || got.LastUsedAt != nil {
		t.Fatalf("GetAPIKey = %+v, %v", got, err)
	}
	used := time.Now()
	if err := f.pg.UpdateAPIKeyLastUsed(f.ctx, k1.ID, used); err != nil {
		t.Fatal(err)
	}
	if got, _ := f.pg.GetAPIKeyByHash(f.ctx, "hash-1"); got.LastUsedAt == nil || !roughly(*got.LastUsedAt, used) {
		t.Errorf("last used = %v", got.LastUsedAt)
	}

	if err := f.pg.RevokeAPIKey(f.ctx, k1.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.pg.GetAPIKeyByHash(f.ctx, "hash-1"); !errors.Is(err, ErrNotFound) {
		t.Errorf("GetAPIKeyByHash(revoked) = %v, want ErrNotFound", err)
	}
	if got, _ := f.pg.GetAPIKey(f.ctx, k1.ID); got.RevokedAt == nil {
		t.Error("revoked key has no revoked_at")
	}

	keys, err := f.pg.ListUserAPIKeys(f.ctx, f.alice.ID)
	if err != nil || len(keys) != 2 {
		t.Errorf("ListUserAPIKeys = %d, %v", len(keys), err)
	}
	if err := f.pg.DeleteAPIKey(f.ctx, k2.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.pg.GetAPIKey(f.ctx, k2.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("GetAPIKey(deleted) = %v", err)
	}
}

func TestHTTPSessions(t *testing.T) {
	f := newFixture(t)
	live := common.NewHTTPSession(f.alice.ID, "tok-live", "10.0.0.1", "curl", time.Now().Add(time.Hour))
	dead := common.NewHTTPSession(f.alice.ID, "tok-dead", "10.0.0.1", "curl", time.Now().Add(-time.Hour))
	for _, s := range []*common.HTTPSession{live, dead} {
		if err := f.pg.CreateHTTPSession(f.ctx, s); err != nil {
			t.Fatal(err)
		}
	}

	if got, err := f.pg.GetHTTPSessionByToken(f.ctx, "tok-live"); err != nil || got.ID != live.ID || got.UserAgent != "curl" {
		t.Errorf("GetHTTPSessionByToken(live) = %+v, %v", got, err)
	}
	if _, err := f.pg.GetHTTPSessionByToken(f.ctx, "tok-dead"); !errors.Is(err, ErrNotFound) {
		t.Errorf("GetHTTPSessionByToken(expired) = %v, want ErrNotFound", err)
	}

	seen := time.Now().Add(time.Minute)
	if err := f.pg.UpdateHTTPSessionLastSeen(f.ctx, live.ID, seen); err != nil {
		t.Fatal(err)
	}
	if got, err := f.pg.GetHTTPSession(f.ctx, live.ID); err != nil || !roughly(got.LastSeenAt, seen) {
		t.Errorf("GetHTTPSession = %+v, %v", got, err)
	}

	if err := f.pg.DeleteExpiredHTTPSessions(f.ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := f.pg.GetHTTPSession(f.ctx, dead.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("expired session survived cleanup: %v", err)
	}
	if err := f.pg.DeleteExpiredHTTPSessions(f.ctx); err != nil {
		t.Errorf("cleanup with nothing to delete = %v", err)
	}
	if err := f.pg.DeleteHTTPSession(f.ctx, live.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.pg.GetHTTPSession(f.ctx, live.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("GetHTTPSession(deleted) = %v", err)
	}
}

func TestSSHKeys(t *testing.T) {
	f := newFixture(t)
	k := &common.SSHKey{ID: "k-1", UserID: f.alice.ID, Name: "yubikey", PublicKey: "sk-ssh-ed25519@openssh.com AAAA", KeyType: "sk-ssh-ed25519@openssh.com", CreatedAt: time.Now()}
	if err := f.pg.CreateSSHKey(f.ctx, k); err != nil {
		t.Fatal(err)
	}
	keys, err := f.pg.ListUserSSHKeys(f.ctx, f.alice.ID)
	if err != nil || len(keys) != 1 || keys[0].KeyType != k.KeyType {
		t.Errorf("ListUserSSHKeys = %+v, %v", keys, err)
	}
	if err := f.pg.DeleteSSHKey(f.ctx, "k-1"); err != nil {
		t.Fatal(err)
	}
	if err := f.pg.DeleteSSHKey(f.ctx, "k-1"); !errors.Is(err, ErrNotFound) {
		t.Errorf("second DeleteSSHKey = %v", err)
	}
}

func TestWebAuthnCredentials(t *testing.T) {
	f := newFixture(t)
	c := &common.WebAuthnCredential{
		ID: "c-1", UserID: f.alice.ID, Name: "laptop", CredentialID: []byte{1, 2, 3}, PublicKey: []byte{9},
		AttestationType: "none", AAGUID: []byte{0}, SignCount: 1, BackupEligible: true, FlagsKnown: true, CreatedAt: time.Now(),
	}
	if err := f.pg.CreateWebAuthnCredential(f.ctx, c); err != nil {
		t.Fatal(err)
	}
	c.SignCount = 7
	c.CloneWarning = true
	c.BackupState = true
	if err := f.pg.UpdateWebAuthnCredential(f.ctx, c); err != nil {
		t.Fatal(err)
	}
	got, err := f.pg.GetWebAuthnCredentialByCredID(f.ctx, []byte{1, 2, 3})
	if err != nil || got.SignCount != 7 || !got.CloneWarning || !got.BackupEligible || !got.BackupState || !got.FlagsKnown {
		t.Errorf("GetWebAuthnCredentialByCredID = %+v, %v", got, err)
	}
	if list, err := f.pg.ListWebAuthnCredentials(f.ctx, f.alice.ID); err != nil || len(list) != 1 {
		t.Errorf("ListWebAuthnCredentials = %d, %v", len(list), err)
	}
	if _, err := f.pg.GetWebAuthnCredentialByCredID(f.ctx, []byte{4}); !errors.Is(err, ErrNotFound) {
		t.Errorf("unknown credential = %v", err)
	}
	if err := f.pg.DeleteWebAuthnCredential(f.ctx, "c-1"); err != nil {
		t.Fatal(err)
	}
	if err := f.pg.DeleteWebAuthnCredential(f.ctx, "c-1"); !errors.Is(err, ErrNotFound) {
		t.Errorf("second delete = %v", err)
	}
}

// --- plugin settings --------------------------------------------------------------------------------

func TestPluginSettings(t *testing.T) {
	f := newFixture(t)
	if _, err := f.pg.GetPluginSetting(f.ctx, "audit"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("GetPluginSetting(unset) = %v", err)
	}
	now := time.Now()
	for _, s := range []*common.PluginSetting{
		{Name: "webhook", Enabled: false, Config: map[string]interface{}{"url": "https://x"}, UpdatedAt: now},
		{Name: "audit", Enabled: true, Config: map[string]interface{}{}, UpdatedAt: now},
		{Name: "audit", Enabled: false, Config: map[string]interface{}{"level": "debug"}, UpdatedAt: now},
	} {
		if err := f.pg.UpsertPluginSetting(f.ctx, s); err != nil {
			t.Fatal(err)
		}
	}
	got, err := f.pg.GetPluginSetting(f.ctx, "audit")
	if err != nil || got.Enabled || got.Config["level"] != "debug" {
		t.Errorf("GetPluginSetting(audit) = %+v, %v; want the second upsert", got, err)
	}
	list, err := f.pg.ListPluginSettings(f.ctx)
	if err != nil || len(list) != 2 || list[0].Name != "audit" || list[1].Config["url"] != "https://x" {
		t.Errorf("ListPluginSettings = %+v, %v", list, err)
	}

	bad := &common.PluginSetting{Name: "bad", Config: map[string]interface{}{"ch": make(chan int)}}
	if err := f.pg.UpsertPluginSetting(f.ctx, bad); err == nil {
		t.Error("UpsertPluginSetting with unencodable config succeeded")
	}
	if _, err := f.pg.db.ExecContext(f.ctx, `INSERT INTO plugin_settings (name, enabled, config, updated_at) VALUES ('arr', true, '[1]', NOW())`); err != nil {
		t.Fatal(err)
	}
	if _, err := f.pg.GetPluginSetting(f.ctx, "arr"); err == nil {
		t.Error("GetPluginSetting with a non-object config succeeded")
	}
	if _, err := f.pg.ListPluginSettings(f.ctx); err == nil {
		t.Error("ListPluginSettings with a non-object config succeeded")
	}
}

// --- SSH CA ----------------------------------------------------------------------------------------

func TestCAKeysAndCertificates(t *testing.T) {
	f := newFixture(t)
	if _, _, err := f.pg.GetActiveCAKey(f.ctx, common.CATypeUser); !errors.Is(err, ErrNotFound) {
		t.Fatalf("GetActiveCAKey(empty) = %v", err)
	}
	old := common.NewCAKey(common.CATypeUser, "ed25519", "ssh-ed25519 OLD", "SHA256:old")
	old.Active = false
	old.RotatedAt = ptr(time.Now())
	old.CreatedAt = time.Now().Add(-time.Hour)
	cur := common.NewCAKey(common.CATypeUser, "ed25519", "ssh-ed25519 NEW", "SHA256:new")
	for _, k := range []*common.CAKey{old, cur} {
		if err := f.pg.CreateCAKey(f.ctx, k, []byte("sealed-"+k.Fingerprint)); err != nil {
			t.Fatal(err)
		}
	}
	second := common.NewCAKey(common.CATypeUser, "ed25519", "x", "y")
	if err := f.pg.CreateCAKey(f.ctx, second, []byte("z")); err == nil {
		t.Error("a second active user CA key was accepted")
	}

	key, priv, err := f.pg.GetActiveCAKey(f.ctx, common.CATypeUser)
	if err != nil || key.ID != cur.ID || string(priv) != "sealed-SHA256:new" {
		t.Errorf("GetActiveCAKey = %+v %q, %v", key, priv, err)
	}
	keys, err := f.pg.ListCAKeys(f.ctx, common.CATypeUser)
	if err != nil || len(keys) != 2 || keys[0].ID != cur.ID || keys[1].RotatedAt == nil {
		t.Errorf("ListCAKeys = %+v, %v", keys, err)
	}

	now := time.Now()
	userCert := common.NewSSHCertificate("1", common.CATypeUser, f.alice.ID, "alice", []string{"alice"}, "fp1", now, now.Add(time.Hour))
	gwCert := common.NewSSHCertificate("2", common.CATypeHost, "", "gw", []string{"gw", "gw.local"}, "fp2", now.Add(time.Second), now.Add(time.Hour))
	expired := common.NewSSHCertificate("3", common.CATypeUser, f.alice.ID, "alice", []string{"alice"}, "fp3", now.Add(-2*time.Hour), now.Add(-time.Hour))
	for _, c := range []*common.SSHCertificate{userCert, gwCert, expired} {
		if err := f.pg.CreateSSHCertificate(f.ctx, c); err != nil {
			t.Fatal(err)
		}
	}
	if err := f.pg.CreateSSHCertificate(f.ctx, userCert); err == nil {
		t.Error("duplicate certificate serial accepted")
	}

	got, err := f.pg.GetSSHCertificateBySerial(f.ctx, "2")
	if err != nil || got.SubjectID != "" || len(got.Principals) != 2 || got.RevokedAt != nil || got.RevokeReason != "" {
		t.Errorf("gateway cert = %+v, %v", got, err)
	}
	if _, err := f.pg.GetSSHCertificateBySerial(f.ctx, "404"); !errors.Is(err, ErrNotFound) {
		t.Errorf("unknown serial = %v", err)
	}

	if err := f.pg.RevokeSSHCertificate(f.ctx, "1", f.admin.ID, "offboarded"); err != nil {
		t.Fatal(err)
	}
	if err := f.pg.RevokeSSHCertificate(f.ctx, "1", f.admin.ID, "again"); !errors.Is(err, ErrNotFound) {
		t.Errorf("revoking twice = %v, want ErrNotFound", err)
	}
	if serials, err := f.pg.ListRevokedCertSerials(f.ctx); err != nil || len(serials) != 1 || serials[0] != "1" {
		t.Errorf("ListRevokedCertSerials = %v, %v", serials, err)
	}
	if got, _ := f.pg.GetSSHCertificateBySerial(f.ctx, "1"); got.RevokeReason != "offboarded" || got.RevokedBy == nil || *got.RevokedBy != f.admin.ID {
		t.Errorf("revoked cert = %+v", got)
	}

	filters := []struct {
		name   string
		filter common.SSHCertFilter
		want   []string
	}{
		{"all", common.SSHCertFilter{}, []string{"2", "1", "3"}},
		{"by type", common.SSHCertFilter{CertType: common.CATypeHost}, []string{"2"}},
		{"by subject", common.SSHCertFilter{SubjectID: f.alice.ID}, []string{"1", "3"}},
		{"active", common.SSHCertFilter{Active: ptr(true)}, []string{"2"}},
		{"inactive user certs", common.SSHCertFilter{CertType: common.CATypeUser, Active: ptr(false)}, []string{"1", "3"}},
	}
	for _, tc := range filters {
		t.Run(tc.name, func(t *testing.T) {
			certs, err := f.pg.ListSSHCertificates(f.ctx, tc.filter, 10, 0)
			if err != nil {
				t.Fatal(err)
			}
			var serials []string
			for _, c := range certs {
				serials = append(serials, c.Serial)
			}
			if len(serials) != len(tc.want) {
				t.Fatalf("serials = %v, want %v", serials, tc.want)
			}
			for i := range serials {
				if serials[i] != tc.want[i] {
					t.Errorf("serials = %v, want %v", serials, tc.want)
				}
			}
		})
	}
}

func TestNullableString(t *testing.T) {
	if nullableString("") != nil || nullableString("x") != "x" {
		t.Error("nullableString should map only the empty string to NULL")
	}
}
