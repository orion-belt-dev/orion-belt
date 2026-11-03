package database

import (
	"context"
	"errors"
	"fmt"
	"os"
	"regexp"
	"strings"
	"time"

	"github.com/lib/pq"
)

// EnvLegacyTimezone names the zone that timestamps in a database created
// before TIMESTAMPTZ were written in, when the server cannot work it out.
const EnvLegacyTimezone = "ORION_DB_LEGACY_TIMEZONE"

// orionTables are the tables this store owns. Only their columns are
// converted, in case the database is shared.
var orionTables = []string{
	"access_requests", "api_keys", "audit_logs", "http_sessions", "machines",
	"notification_policy", "notification_prefs", "notifications", "notification_templates",
	"permission_history", "permissions", "plugin_settings", "risk_policy", "sessions",
	"ssh_ca_keys", "ssh_certificates", "users", "user_ssh_keys", "webauthn_credentials",
}

// readLocaltime resolves /etc/localtime; a variable so tests need not depend
// on the host.
var readLocaltime = func() (string, error) { return os.Readlink("/etc/localtime") }

// zoneName is what a zone passed to Postgres may look like (an IANA name
// such as Europe/Paris, or UTC); it is quoted into DDL, so nothing else.
var zoneName = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_+\-]*(/[A-Za-z0-9_+\-]+)*$`)

// legacyTimestampColumns lists the TIMESTAMP (without time zone) columns
// left in this store's tables, by table.
func (s *PostgresStore) legacyTimestampColumns(ctx context.Context) (map[string][]string, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT table_name, column_name FROM information_schema.columns
		WHERE table_schema = current_schema() AND data_type = 'timestamp without time zone'
		  AND table_name = ANY($1)
		ORDER BY table_name, ordinal_position`, pq.Array(orionTables))
	if err != nil {
		return nil, fmt.Errorf("list timestamp columns: %w", err)
	}
	defer rows.Close()
	cols := map[string][]string{}
	for rows.Next() {
		var table, column string
		if err := rows.Scan(&table, &column); err != nil {
			return nil, fmt.Errorf("scan timestamp column: %w", err)
		}
		cols[table] = append(cols[table], column)
	}
	return cols, rows.Err()
}

// migrateTimestampsToTZ converts the legacy TIMESTAMP columns to
// TIMESTAMPTZ. They hold the wall clock of the server that wrote them, so
// Postgres compared them with NOW() in its own session zone and expiry
// drifted whenever the two zones differed. Each table is rewritten once,
// under an exclusive lock, on the first start after the upgrade; later
// starts find nothing to do.
func (s *PostgresStore) migrateTimestampsToTZ(ctx context.Context) error {
	cols, err := s.legacyTimestampColumns(ctx)
	if err != nil || len(cols) == 0 {
		return err
	}
	zone, err := legacyTimestampZone()
	if err != nil {
		return err
	}
	return s.convertLegacyTimestamps(ctx, cols, zone)
}

func (s *PostgresStore) convertLegacyTimestamps(ctx context.Context, cols map[string][]string, zone string) error {
	if !zoneName.MatchString(zone) {
		return fmt.Errorf("legacy timestamp zone %q is not a zone name", zone)
	}
	var probe time.Time
	if err := s.db.QueryRowContext(ctx, `SELECT TIMESTAMP '2000-01-01' AT TIME ZONE $1`, zone).Scan(&probe); err != nil {
		return fmt.Errorf("postgres does not know legacy timestamp zone %q (set %s): %w", zone, EnvLegacyTimezone, err)
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("convert timestamps: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	for _, table := range orionTables {
		if len(cols[table]) == 0 {
			continue
		}
		clauses := make([]string, 0, len(cols[table]))
		for _, c := range cols[table] {
			clauses = append(clauses, fmt.Sprintf(`ALTER COLUMN %q TYPE TIMESTAMPTZ USING %q AT TIME ZONE '%s'`, c, c, zone))
		}
		if _, err := tx.ExecContext(ctx, fmt.Sprintf(`ALTER TABLE %q %s`, table, strings.Join(clauses, ", "))); err != nil {
			return fmt.Errorf("convert %s timestamps: %w", table, err)
		}
	}
	return tx.Commit()
}

// legacyTimestampZone is the zone the server wrote legacy timestamps in:
// EnvLegacyTimezone if set, UTC on a UTC host (containers and most
// servers), else TZ or the /etc/localtime link. It refuses to guess
// beyond that, since a wrong zone shifts every stored time.
func legacyTimestampZone() (string, error) {
	if z := strings.TrimSpace(os.Getenv(EnvLegacyTimezone)); z != "" {
		return z, nil
	}
	if alwaysUTC(time.Local) {
		return "UTC", nil
	}
	if tz := strings.TrimPrefix(os.Getenv("TZ"), ":"); tz != "" {
		if _, err := time.LoadLocation(tz); err == nil && zoneName.MatchString(tz) {
			return tz, nil
		}
	}
	if target, err := readLocaltime(); err == nil {
		if i := strings.Index(target, "zoneinfo/"); i >= 0 && zoneName.MatchString(target[i+len("zoneinfo/"):]) {
			return target[i+len("zoneinfo/"):], nil
		}
	}
	return "", errors.New("cannot tell which time zone existing timestamps were written in; set " +
		EnvLegacyTimezone + " to the server's zone (for example Europe/Paris) and start again")
}

// alwaysUTC reports whether loc has no offset in winter or summer.
func alwaysUTC(loc *time.Location) bool {
	year := time.Now().Year()
	for _, m := range []time.Month{time.January, time.July} {
		if _, off := time.Date(year, m, 1, 0, 0, 0, 0, loc).Zone(); off != 0 {
			return false
		}
	}
	return true
}
