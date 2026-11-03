package database

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/orion-belt-dev/orion-belt/internal/pgtest"
	"github.com/orion-belt-dev/orion-belt/pkg/common"
)

// schemaStore opens a migrated store in a schema of its own, with extra
// connection parameters (such as the session time zone).
func schemaStore(t *testing.T, params string) (*PostgresStore, *sql.DB) {
	t.Helper()
	base := pgtest.DSN(t)
	admin, err := sql.Open("postgres", base)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = admin.Close() })
	schema := "tz_" + strings.ReplaceAll(uuid.NewString()[:8], "-", "")
	if _, err := admin.Exec(`CREATE SCHEMA ` + schema); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = admin.Exec(`DROP SCHEMA ` + schema + ` CASCADE`) })

	sep := "?"
	if strings.Contains(base, "?") {
		sep = "&"
	}
	st, err := NewPostgresStore(base + sep + "search_path=" + schema + params)
	if err != nil {
		t.Fatal(err)
	}
	pg := st.(*PostgresStore)
	t.Cleanup(func() { _ = pg.Close() })
	if err := pg.Migrate(context.Background()); err != nil {
		t.Fatal(err)
	}
	return pg, pg.db
}

func TestMigrate_CreatesOnlyTimestampTZColumns(t *testing.T) {
	pg, _ := schemaStore(t, "")
	cols, err := pg.legacyTimestampColumns(context.Background())
	if err != nil || len(cols) != 0 {
		t.Errorf("legacy timestamp columns on a fresh schema = %v, %v", cols, err)
	}
}

// Expiry is compared with NOW() in SQL. With wall-clock columns that only
// worked while Postgres's session zone matched the server's.
func TestGrantExpiry_IndependentOfSessionTimeZone(t *testing.T) {
	pg, _ := schemaStore(t, "&timezone=Pacific/Kiritimati") // UTC+14
	ctx := context.Background()
	u := common.NewUser("alice", "a@x", "ssh-ed25519 AAAA", false)
	m := common.NewMachine("web-01", "10.0.0.1", 22, nil)
	if err := pg.CreateUser(ctx, u); err != nil {
		t.Fatal(err)
	}
	if err := pg.CreateMachine(ctx, m); err != nil {
		t.Fatal(err)
	}
	soon := time.Now().UTC().Add(time.Hour)
	gone := time.Now().UTC().Add(-time.Hour)
	for _, exp := range []*time.Time{&soon, &gone} {
		if err := pg.CreatePermission(ctx, common.NewPermission(u.ID, m.ID, "ssh", []string{"root"}, "", exp)); err != nil {
			t.Fatal(err)
		}
	}
	perms, err := pg.ListUserPermissions(ctx, u.ID)
	if err != nil {
		t.Fatal(err)
	}
	// Postgres keeps microseconds, rounded.
	if len(perms) != 1 || perms[0].ExpiresAt.Sub(soon).Abs() > time.Microsecond {
		t.Errorf("active grants = %+v, want only the one expiring at %s", perms, soon)
	}
}

func TestMigrateTimestampsToTZ_ConvertsLegacyWallClock(t *testing.T) {
	pg, db := schemaStore(t, "")
	ctx := context.Background()
	u := common.NewUser("alice", "a@x", "ssh-ed25519 AAAA", false)
	m := common.NewMachine("web-01", "10.0.0.1", 22, nil)
	_ = pg.CreateUser(ctx, u)
	_ = pg.CreateMachine(ctx, m)

	// Back to the old schema, holding a Paris server's wall clock.
	for _, stmt := range []string{
		`ALTER TABLE permissions ALTER COLUMN granted_at TYPE TIMESTAMP, ALTER COLUMN expires_at TYPE TIMESTAMP`,
		`ALTER TABLE audit_logs ALTER COLUMN timestamp TYPE TIMESTAMP`,
	} {
		if _, err := db.Exec(stmt); err != nil {
			t.Fatal(err)
		}
	}
	id := uuid.NewString()
	if _, err := db.Exec(`INSERT INTO permissions (id, user_id, machine_id, access_type, remote_users, granted_at, expires_at)
		VALUES ($1, $2, $3, 'ssh', '{root}', TIMESTAMP '2026-07-01 12:00:00', TIMESTAMP '2026-12-01 10:00:00')`, id, u.ID, m.ID); err != nil {
		t.Fatal(err)
	}

	cols, err := pg.legacyTimestampColumns(ctx)
	if err != nil || fmt.Sprint(cols) != "map[audit_logs:[timestamp] permissions:[granted_at expires_at]]" {
		t.Fatalf("legacy columns = %v, %v", cols, err)
	}
	for _, bad := range []string{"Mars/Olympus_Mons", "UTC'; DROP TABLE users; --"} {
		if err := pg.convertLegacyTimestamps(ctx, cols, bad); err == nil {
			t.Errorf("zone %q accepted", bad)
		}
	}
	// A failing ALTER leaves every table as it was (one transaction).
	broken := map[string][]string{"audit_logs": {"timestamp"}, "permissions": {"no_such_column"}}
	if err := pg.convertLegacyTimestamps(ctx, broken, "Europe/Paris"); err == nil {
		t.Error("ALTER of a missing column succeeded")
	}
	if still, _ := pg.legacyTimestampColumns(ctx); fmt.Sprint(still) != fmt.Sprint(cols) {
		t.Errorf("columns after a failed conversion = %v, want %v", still, cols)
	}
	if err := pg.convertLegacyTimestamps(ctx, cols, "Europe/Paris"); err != nil {
		t.Fatal(err)
	}

	p, err := pg.GetPermission(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	// 10:00 in Paris in December (CET) and 12:00 in July (CEST).
	if want := time.Date(2026, 12, 1, 9, 0, 0, 0, time.UTC); !p.ExpiresAt.Equal(want) {
		t.Errorf("expires_at = %s, want %s", p.ExpiresAt.UTC(), want)
	}
	if want := time.Date(2026, 7, 1, 10, 0, 0, 0, time.UTC); !p.GrantedAt.Equal(want) {
		t.Errorf("granted_at = %s, want %s", p.GrantedAt.UTC(), want)
	}
	if cols, _ := pg.legacyTimestampColumns(ctx); len(cols) != 0 {
		t.Errorf("columns left after conversion: %v", cols)
	}
	// Later starts have nothing to do and need no zone.
	t.Setenv(EnvLegacyTimezone, "Not/A_Zone")
	if err := pg.Migrate(ctx); err != nil {
		t.Errorf("second Migrate: %v", err)
	}
}

func TestLegacyTimestampZone(t *testing.T) {
	t.Setenv(EnvLegacyTimezone, "Europe/Paris")
	if z, err := legacyTimestampZone(); err != nil || z != "Europe/Paris" {
		t.Errorf("override = %q, %v", z, err)
	}
	t.Setenv(EnvLegacyTimezone, "")
	withLocal(t, time.UTC, "")
	if z, err := legacyTimestampZone(); err != nil || z != "UTC" {
		t.Errorf("UTC host = %q, %v", z, err)
	}
	if !alwaysUTC(time.UTC) || alwaysUTC(time.FixedZone("CET", 3600)) {
		t.Error("alwaysUTC misjudges a zone")
	}
	if paris, err := time.LoadLocation("Europe/Paris"); err == nil && alwaysUTC(paris) {
		t.Error("alwaysUTC(Europe/Paris)")
	}
}

// withLocal runs the zone lookup as on a host in loc whose /etc/localtime
// links to link (an empty link: no link).
func withLocal(t *testing.T, loc *time.Location, link string) {
	t.Helper()
	prevLocal, prevRead := time.Local, readLocaltime
	time.Local = loc
	readLocaltime = func() (string, error) {
		if link == "" {
			return "", os.ErrNotExist
		}
		return link, nil
	}
	t.Cleanup(func() { time.Local, readLocaltime = prevLocal, prevRead })
}

func TestLegacyTimestampZone_NonUTCHost(t *testing.T) {
	paris, err := time.LoadLocation("Europe/Paris")
	if err != nil {
		t.Skip("no tzdata")
	}
	t.Setenv(EnvLegacyTimezone, "")

	withLocal(t, paris, "/usr/share/zoneinfo/Europe/Paris")
	t.Setenv("TZ", ":America/New_York")
	if z, err := legacyTimestampZone(); err != nil || z != "America/New_York" {
		t.Errorf("TZ = %q, %v", z, err)
	}
	t.Setenv("TZ", "Not/A_Zone")
	if z, err := legacyTimestampZone(); err != nil || z != "Europe/Paris" {
		t.Errorf("/etc/localtime = %q, %v", z, err)
	}

	withLocal(t, paris, "")
	t.Setenv("TZ", "")
	if z, err := legacyTimestampZone(); err == nil || !strings.Contains(err.Error(), EnvLegacyTimezone) {
		t.Errorf("undecidable zone = %q, %v; want an error naming %s", z, err, EnvLegacyTimezone)
	}
}

// Migrate converts on its own, and refuses to start rather than guess.
func TestMigrate_ConvertsOrStopsOnLegacyColumns(t *testing.T) {
	paris, err := time.LoadLocation("Europe/Paris")
	if err != nil {
		t.Skip("no tzdata")
	}
	pg, db := schemaStore(t, "")
	ctx := context.Background()
	if _, err := db.Exec(`ALTER TABLE sessions ALTER COLUMN start_time TYPE TIMESTAMP`); err != nil {
		t.Fatal(err)
	}

	t.Setenv(EnvLegacyTimezone, "")
	t.Setenv("TZ", "")
	withLocal(t, paris, "")
	if err := pg.Migrate(ctx); err == nil || !strings.Contains(err.Error(), EnvLegacyTimezone) {
		t.Fatalf("Migrate with an unknown legacy zone = %v", err)
	}

	t.Setenv(EnvLegacyTimezone, "Europe/Paris")
	if err := pg.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	if cols, _ := pg.legacyTimestampColumns(ctx); len(cols) != 0 {
		t.Errorf("columns left: %v", cols)
	}
}

func TestTimestampMigration_StoreFailures(t *testing.T) {
	pg, _ := schemaStore(t, "")
	ctx := context.Background()
	_ = pg.Close()
	if _, err := pg.legacyTimestampColumns(ctx); err == nil {
		t.Error("legacyTimestampColumns on a closed store")
	}
	if err := pg.migrateTimestampsToTZ(ctx); err == nil {
		t.Error("migrateTimestampsToTZ on a closed store")
	}
	if err := pg.convertLegacyTimestamps(ctx, map[string][]string{"users": {"created_at"}}, "UTC"); err == nil {
		t.Error("convertLegacyTimestamps on a closed store")
	}
}

func TestReadLocaltime_HostLink(t *testing.T) {
	target, err := readLocaltime()
	if err == nil && target == "" {
		t.Error("readLocaltime returned an empty link without an error")
	}
}
