package database

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/orion-belt-dev/orion-belt/pkg/common"
	"github.com/orion-belt-dev/orion-belt/pkg/notify"
)

// closedStore returns a store whose pool is already closed, so every call
// fails at the driver. Only needs a DSN, not a reachable server.
func closedStore(t *testing.T) *PostgresStore {
	t.Helper()
	dsn := os.Getenv("ORION_TEST_POSTGRES_DSN")
	if dsn == "" {
		dsn = "postgres://user@127.0.0.1:1/db?sslmode=disable"
	}
	s, err := NewPostgresStore(dsn)
	if err != nil {
		t.Fatal(err)
	}
	pg := s.(*PostgresStore)
	_ = pg.Close()
	return pg
}

// Every store method must surface a driver failure as an error rather than
// a zero value that callers would read as "not found" or "no rows".
func TestEveryMethodReportsDriverFailure(t *testing.T) {
	pg := closedStore(t)
	ctx := context.Background()
	now := time.Now()
	u := common.NewUser("u", "e", "", false)
	m := common.NewMachine("m", "h", 22, nil)

	calls := map[string]func() error{
		"Connect":            func() error { return pg.Connect(ctx) },
		"Ping":               func() error { return pg.Ping(ctx) },
		"Migrate":            func() error { return pg.Migrate(ctx) },
		"CreateUser":         func() error { return pg.CreateUser(ctx, u) },
		"CreateFirstUser":    func() error { return pg.CreateFirstUser(ctx, u) },
		"GetUser":            func() error { _, err := pg.GetUser(ctx, "x"); return err },
		"GetUserByUsername":  func() error { _, err := pg.GetUserByUsername(ctx, "x"); return err },
		"UpdateUser":         func() error { return pg.UpdateUser(ctx, u) },
		"DeleteUser":         func() error { return pg.DeleteUser(ctx, "x") },
		"ListUsers":          func() error { _, err := pg.ListUsers(ctx, 1, 0); return err },
		"CreateMachine":      func() error { return pg.CreateMachine(ctx, m) },
		"GetMachine":         func() error { _, err := pg.GetMachine(ctx, "x"); return err },
		"GetMachineByName":   func() error { _, err := pg.GetMachineByName(ctx, "x"); return err },
		"UpdateMachine":      func() error { return pg.UpdateMachine(ctx, m) },
		"DeleteMachine":      func() error { return pg.DeleteMachine(ctx, "x") },
		"ListMachines":       func() error { _, err := pg.ListMachines(ctx, 1, 0); return err },
		"ListActiveMachines": func() error { _, err := pg.ListActiveMachines(ctx); return err },
		"ListSessionRecordingPathsForMachine": func() error {
			_, err := pg.ListSessionRecordingPathsForMachine(ctx, "x")
			return err
		},
		"CreateSession":      func() error { return pg.CreateSession(ctx, &common.Session{}) },
		"GetSession":         func() error { _, err := pg.GetSession(ctx, "x"); return err },
		"UpdateSession":      func() error { return pg.UpdateSession(ctx, &common.Session{}) },
		"ListSessions":       func() error { _, err := pg.ListSessions(ctx, 1, 0); return err },
		"ListActiveSessions": func() error { _, err := pg.ListActiveSessions(ctx); return err },
		"ListUserSessions":   func() error { _, err := pg.ListUserSessions(ctx, "x", 1, 0); return err },
		"EndSession":         func() error { return pg.EndSession(ctx, "x", now) },
		"GetSessionDetails":  func() error { _, err := pg.GetSessionDetails(ctx, "x"); return err },
		"ListUserSessionsWithMachineNames": func() error {
			_, err := pg.ListUserSessionsWithMachineNames(ctx, "x", 1, 0)
			return err
		},
		"CreateAccessRequest":       func() error { return pg.CreateAccessRequest(ctx, &common.AccessRequest{}) },
		"GetAccessRequest":          func() error { _, err := pg.GetAccessRequest(ctx, "x"); return err },
		"UpdateAccessRequest":       func() error { return pg.UpdateAccessRequest(ctx, &common.AccessRequest{}) },
		"ListPendingAccessRequests": func() error { _, err := pg.ListPendingAccessRequests(ctx); return err },
		"ListAllAccessRequests":     func() error { _, err := pg.ListAllAccessRequests(ctx, 1, 0); return err },
		"ListUserAccessRequests":    func() error { _, err := pg.ListUserAccessRequests(ctx, "x", 1, 0); return err },
		"ExpireStalePendingAccessRequests": func() error {
			_, err := pg.ExpireStalePendingAccessRequests(ctx, time.Hour)
			return err
		},
		"CreatePermission":       func() error { return pg.CreatePermission(ctx, &common.Permission{}) },
		"GetPermission":          func() error { _, err := pg.GetPermission(ctx, "x"); return err },
		"UpdatePermission":       func() error { return pg.UpdatePermission(ctx, &common.Permission{}) },
		"DeletePermission":       func() error { return pg.DeletePermission(ctx, "x") },
		"ListUserPermissions":    func() error { _, err := pg.ListUserPermissions(ctx, "x"); return err },
		"ListMachinePermissions": func() error { _, err := pg.ListMachinePermissions(ctx, "x"); return err },
		"ListAllPermissions":     func() error { _, err := pg.ListAllPermissions(ctx, 1, 0); return err },
		"ListPermissionVersions": func() error { _, err := pg.ListPermissionVersions(ctx, 1); return err },
		"ListPairPermissions":    func() error { _, err := pg.ListPairPermissions(ctx, "u", "m"); return err },
		"FindActivePermission":   func() error { _, err := pg.FindActivePermission(ctx, "u", "m", "ssh"); return err },
		"HasPermission":          func() error { _, err := pg.HasPermission(ctx, "u", "m", "ssh"); return err },
		"HasPermissionWithRemoteUser": func() error {
			_, err := pg.HasPermissionWithRemoteUser(ctx, "u", "m", "ssh", "root")
			return err
		},
		"CreateAuditLog":     func() error { return pg.CreateAuditLog(ctx, &common.AuditLog{}) },
		"ListAuditLogs":      func() error { _, err := pg.ListAuditLogs(ctx, 1, 0, nil); return err },
		"CreateNotification": func() error { return pg.CreateNotification(ctx, &common.Notification{}) },
		"ListUserNotifications": func() error {
			_, err := pg.ListUserNotifications(ctx, "x", 1, 0)
			return err
		},
		"CountUnreadNotifications": func() error { _, err := pg.CountUnreadNotifications(ctx, "x"); return err },
		"MarkNotificationRead":     func() error { return pg.MarkNotificationRead(ctx, "x", "u") },
		"MarkAllNotificationsRead": func() error { return pg.MarkAllNotificationsRead(ctx, "u") },
		"GetNotificationPrefs":     func() error { _, err := pg.GetNotificationPrefs(ctx, "u"); return err },
		"UpsertNotificationPrefs": func() error {
			return pg.UpsertNotificationPrefs(ctx, &common.NotificationPrefs{})
		},
		"GetNotificationPolicy": func() error { _, err := pg.GetNotificationPolicy(ctx); return err },
		"UpsertNotificationPolicy": func() error {
			return pg.UpsertNotificationPolicy(ctx, &common.NotificationPolicy{})
		},
		"ListNotificationTemplates": func() error { _, err := pg.ListNotificationTemplates(ctx); return err },
		"UpsertNotificationTemplate": func() error {
			return pg.UpsertNotificationTemplate(ctx, &notify.Template{})
		},
		"DeleteNotificationTemplate": func() error { return pg.DeleteNotificationTemplate(ctx, "x") },
		"CreateAPIKey":               func() error { return pg.CreateAPIKey(ctx, &common.APIKey{}) },
		"GetAPIKey":                  func() error { _, err := pg.GetAPIKey(ctx, "x"); return err },
		"GetAPIKeyByHash":            func() error { _, err := pg.GetAPIKeyByHash(ctx, "x"); return err },
		"ListUserAPIKeys":            func() error { _, err := pg.ListUserAPIKeys(ctx, "x"); return err },
		"UpdateAPIKeyLastUsed":       func() error { return pg.UpdateAPIKeyLastUsed(ctx, "x", now) },
		"RevokeAPIKey":               func() error { return pg.RevokeAPIKey(ctx, "x") },
		"DeleteAPIKey":               func() error { return pg.DeleteAPIKey(ctx, "x") },
		"CreateHTTPSession":          func() error { return pg.CreateHTTPSession(ctx, &common.HTTPSession{}) },
		"GetHTTPSession":             func() error { _, err := pg.GetHTTPSession(ctx, "x"); return err },
		"GetHTTPSessionByToken":      func() error { _, err := pg.GetHTTPSessionByToken(ctx, "x"); return err },
		"UpdateHTTPSessionLastSeen":  func() error { return pg.UpdateHTTPSessionLastSeen(ctx, "x", now) },
		"DeleteHTTPSession":          func() error { return pg.DeleteHTTPSession(ctx, "x") },
		"DeleteExpiredHTTPSessions":  func() error { return pg.DeleteExpiredHTTPSessions(ctx) },
		"CreateSSHKey":               func() error { return pg.CreateSSHKey(ctx, &common.SSHKey{}) },
		"ListUserSSHKeys":            func() error { _, err := pg.ListUserSSHKeys(ctx, "x"); return err },
		"DeleteSSHKey":               func() error { return pg.DeleteSSHKey(ctx, "x") },
		"CreateWebAuthnCredential": func() error {
			return pg.CreateWebAuthnCredential(ctx, &common.WebAuthnCredential{})
		},
		"ListWebAuthnCredentials": func() error { _, err := pg.ListWebAuthnCredentials(ctx, "x"); return err },
		"GetWebAuthnCredentialByCredID": func() error {
			_, err := pg.GetWebAuthnCredentialByCredID(ctx, []byte{1})
			return err
		},
		"UpdateWebAuthnCredential": func() error {
			return pg.UpdateWebAuthnCredential(ctx, &common.WebAuthnCredential{})
		},
		"DeleteWebAuthnCredential": func() error { return pg.DeleteWebAuthnCredential(ctx, "x") },
		"GetPluginSetting":         func() error { _, err := pg.GetPluginSetting(ctx, "x"); return err },
		"ListPluginSettings":       func() error { _, err := pg.ListPluginSettings(ctx); return err },
		"UpsertPluginSetting":      func() error { return pg.UpsertPluginSetting(ctx, &common.PluginSetting{}) },
		"CreateCAKey":              func() error { return pg.CreateCAKey(ctx, &common.CAKey{}, nil) },
		"GetActiveCAKey":           func() error { _, _, err := pg.GetActiveCAKey(ctx, "user"); return err },
		"ListCAKeys":               func() error { _, err := pg.ListCAKeys(ctx, "user"); return err },
		"CreateSSHCertificate":     func() error { return pg.CreateSSHCertificate(ctx, &common.SSHCertificate{}) },
		"GetSSHCertificateBySerial": func() error {
			_, err := pg.GetSSHCertificateBySerial(ctx, "1")
			return err
		},
		"ListSSHCertificates": func() error {
			_, err := pg.ListSSHCertificates(ctx, common.SSHCertFilter{}, 1, 0)
			return err
		},
		"ListRevokedCertSerials": func() error { _, err := pg.ListRevokedCertSerials(ctx); return err },
		"RevokeSSHCertificate":   func() error { return pg.RevokeSSHCertificate(ctx, "1", "u", "r") },
	}
	for name, call := range calls {
		t.Run(name, func(t *testing.T) {
			err := call()
			if err == nil {
				t.Fatalf("%s on a closed pool returned nil", name)
			}
			if errors.Is(err, ErrNotFound) {
				t.Errorf("%s reported a driver failure as ErrNotFound", name)
			}
		})
	}
}

func TestMigrate_StatementFailureIsWrapped(t *testing.T) {
	err := closedStore(t).Migrate(context.Background())
	if !errors.Is(err, ErrMigrationFailed) {
		t.Errorf("Migrate = %v, want ErrMigrationFailed", err)
	}
}

// --- schema repair -----------------------------------------------------------------

func fkRule(t *testing.T, pg *PostgresStore, table, column string) []existingFK {
	t.Helper()
	fks, err := pg.listForeignKeys(context.Background(), table, column)
	if err != nil {
		t.Fatal(err)
	}
	return fks
}

// Upgraded databases carry FKs without ON DELETE rules, under old names and
// sometimes twice. Migrate must converge them on the rules this build needs.
func TestMigrate_RepairsDriftedForeignKeys(t *testing.T) {
	pg := openTestPostgres(t)
	ctx := context.Background()
	drift := []string{
		// No delete rule (NO ACTION) under the expected name.
		`ALTER TABLE sessions DROP CONSTRAINT sessions_machine_id_fkey`,
		`ALTER TABLE sessions ADD CONSTRAINT sessions_machine_id_fkey FOREIGN KEY (machine_id) REFERENCES machines(id)`,
		// Two constraints on one column, one under a legacy name.
		`ALTER TABLE api_keys ADD CONSTRAINT legacy_api_keys_user FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE RESTRICT`,
		// Wrong rule: SET DEFAULT where SET NULL is wanted.
		`ALTER TABLE audit_logs DROP CONSTRAINT audit_logs_user_id_fkey`,
		`ALTER TABLE audit_logs ADD CONSTRAINT audit_logs_user_id_fkey FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE SET DEFAULT`,
		// Missing entirely.
		`ALTER TABLE notifications DROP CONSTRAINT notifications_user_id_fkey`,
		// Pre-migration NOT NULL.
		`ALTER TABLE permissions ALTER COLUMN granted_by SET NOT NULL`,
	}
	for _, stmt := range drift {
		if _, err := pg.db.ExecContext(ctx, stmt); err != nil {
			t.Fatalf("%s: %v", stmt, err)
		}
	}

	if err := pg.Migrate(ctx); err != nil {
		t.Fatalf("Migrate on a drifted schema: %v", err)
	}

	want := map[[2]string]string{
		{"sessions", "machine_id"}:   "CASCADE",
		{"api_keys", "user_id"}:      "CASCADE",
		{"audit_logs", "user_id"}:    "SET NULL",
		{"notifications", "user_id"}: "CASCADE",
	}
	for col, rule := range want {
		fks := fkRule(t, pg, col[0], col[1])
		if len(fks) != 1 || fks[0].deleteRule != rule || fks[0].name != col[0]+"_"+col[1]+"_fkey" {
			t.Errorf("%s.%s FKs = %+v, want one %s constraint under the standard name", col[0], col[1], fks, rule)
		}
	}
	var nullable string
	if err := pg.db.QueryRowContext(ctx, `SELECT is_nullable FROM information_schema.columns
		WHERE table_name='permissions' AND column_name='granted_by'`).Scan(&nullable); err != nil || nullable != "YES" {
		t.Errorf("permissions.granted_by nullable = %q, %v; want YES", nullable, err)
	}
}

func TestEnsureForeignKey_OtherDeleteRules(t *testing.T) {
	pg := openTestPostgres(t)
	ctx := context.Background()
	if _, err := pg.db.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS fk_probe (id VARCHAR(36), user_id VARCHAR(36) DEFAULT NULL)`); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = pg.db.ExecContext(ctx, `DROP TABLE IF EXISTS fk_probe`) })

	for _, rule := range []string{"RESTRICT", "SET DEFAULT", "NO ACTION"} {
		spec := fkSpec{"fk_probe", "user_id", "users", "id", rule, "fk_probe_user_id_fkey"}
		if err := pg.ensureForeignKey(ctx, spec); err != nil {
			t.Fatalf("ensureForeignKey(%s): %v", rule, err)
		}
		fks := fkRule(t, pg, "fk_probe", "user_id")
		if len(fks) != 1 || fks[0].deleteRule != rule {
			t.Errorf("after ensureForeignKey(%s) FKs = %+v", rule, fks)
		}
	}

	bad := fkSpec{"fk_probe", "user_id", "no_such_table", "id", "CASCADE", "fk_probe_user_id_fkey"}
	if err := pg.ensureForeignKey(ctx, bad); err == nil || !strings.Contains(err.Error(), "add FK") {
		t.Errorf("ensureForeignKey to a missing table = %v, want an add FK error", err)
	}
}

func TestEnsureColumnNullable_MissingColumnIsNoOp(t *testing.T) {
	pg := openTestPostgres(t)
	if err := pg.ensureColumnNullable(context.Background(), "no_such_table", "x"); err != nil {
		t.Errorf("ensureColumnNullable(missing) = %v, want nil", err)
	}
}

func TestSchemaRepairHelpers_DriverFailure(t *testing.T) {
	pg := closedStore(t)
	ctx := context.Background()
	if err := pg.ensureColumnNullable(ctx, "permissions", "granted_by"); err == nil || !strings.Contains(err.Error(), "inspect") {
		t.Errorf("ensureColumnNullable = %v", err)
	}
	if err := pg.ensureForeignKey(ctx, fkSpec{"a", "b", "c", "d", "CASCADE", "n"}); err == nil || !strings.Contains(err.Error(), "inspect FK") {
		t.Errorf("ensureForeignKey = %v", err)
	}
	if err := pg.repairSchema(ctx); err == nil {
		t.Error("repairSchema on a closed pool succeeded")
	}
}

// With a table renamed away, each step of the cascading deletes reports which
// dependent rows it could not remove, and the transaction leaves the parent.
func TestCascadingDeletes_ReportFailingStep(t *testing.T) {
	cases := []struct {
		table, want string
		del         func(f *fixture) error
	}{
		{"access_requests", "failed to clear access-request reviewer", func(f *fixture) error { return f.pg.DeleteUser(f.ctx, f.alice.ID) }},
		{"audit_logs", "failed to clear audit log user", func(f *fixture) error { return f.pg.DeleteUser(f.ctx, f.alice.ID) }},
		{"ssh_certificates", "failed to clear certificate revoker", func(f *fixture) error { return f.pg.DeleteUser(f.ctx, f.alice.ID) }},
		{"sessions", "failed to delete user sessions", func(f *fixture) error { return f.pg.DeleteUser(f.ctx, f.alice.ID) }},
		{"permissions", "failed to delete machine permissions", func(f *fixture) error { return f.pg.DeleteMachine(f.ctx, f.machine.ID) }},
		{"access_requests", "failed to delete machine access requests", func(f *fixture) error { return f.pg.DeleteMachine(f.ctx, f.machine.ID) }},
		{"sessions", "failed to delete machine sessions", func(f *fixture) error { return f.pg.DeleteMachine(f.ctx, f.machine.ID) }},
	}
	for _, tc := range cases {
		t.Run(tc.want, func(t *testing.T) {
			f := newFixture(t)
			if _, err := f.pg.db.ExecContext(f.ctx, `ALTER TABLE `+tc.table+` RENAME TO `+tc.table+`_away`); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				_, _ = f.pg.db.ExecContext(f.ctx, `ALTER TABLE `+tc.table+`_away RENAME TO `+tc.table)
			})
			if err := tc.del(f); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Errorf("delete = %v, want %q", err, tc.want)
			}
		})
	}
}
