// Package anomaly flags unusual access for review. It never blocks: a flag
// is an audit entry naming the signal that triggered it, so a reviewer can
// see exactly why it was raised.
package anomaly

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/orion-belt-dev/orion-belt/pkg/common"
)

// Kinds of anomaly, recorded as the audit action ActionPrefix + kind.
const (
	ActionPrefix    = "anomaly."
	KindFirstAccess = "first_time_access"
	KindOffHours    = "off_hours"
)

const (
	// historyLimit bounds how many of the user's past sessions are read.
	historyLimit = 500
	// minHistory is how many past sessions a user needs before their
	// "normal hours" mean anything; below it off-hours is not judged.
	minHistory = 10
	// hourTolerance widens the usual hours so starting a little earlier or
	// later than ever before is not flagged.
	hourTolerance = 1
)

// Store is the subset of database.Store the detector needs.
type Store interface {
	ListUserSessions(ctx context.Context, userID string, limit, offset int) ([]*common.Session, error)
	CreateAuditLog(ctx context.Context, log *common.AuditLog) error
}

// Flag is one anomaly raised for a session.
type Flag struct {
	Kind   string
	Signal string // human-readable trigger, e.g. "first session on db-01 (sensitivity high)"
}

// Detect returns the anomalies for session, judged against the user's
// earlier sessions. machine is the session's target.
func Detect(ctx context.Context, store Store, session *common.Session, machine *common.Machine) ([]Flag, error) {
	history, err := store.ListUserSessions(ctx, session.UserID, historyLimit, 0)
	if err != nil {
		return nil, fmt.Errorf("list sessions of user %s: %w", session.UserID, err)
	}
	var past []*common.Session
	for _, h := range history {
		if h.ID != session.ID {
			past = append(past, h)
		}
	}

	var flags []Flag
	if f, ok := firstAccess(session, machine, past); ok {
		flags = append(flags, f)
	}
	if f, ok := offHours(session, past); ok {
		flags = append(flags, f)
	}
	return flags, nil
}

// firstAccess flags the first session a user opens on a high-sensitivity
// machine.
func firstAccess(session *common.Session, machine *common.Machine, past []*common.Session) (Flag, bool) {
	sensitivity := common.MachineSensitivity(machine)
	if sensitivity != common.SensitivityHigh {
		return Flag{}, false
	}
	for _, p := range past {
		if p.MachineID == session.MachineID {
			return Flag{}, false
		}
	}
	return Flag{
		Kind:   KindFirstAccess,
		Signal: fmt.Sprintf("first session by this user on %s (sensitivity %s)", machine.Name, sensitivity),
	}, true
}

// offHours flags a session that starts outside every hour (UTC, with
// tolerance) the user has started a session in before.
func offHours(session *common.Session, past []*common.Session) (Flag, bool) {
	if len(past) < minHistory {
		return Flag{}, false
	}
	usual := map[int]bool{}
	for _, p := range past {
		usual[common.FromDBTimestamp(p.StartTime).Hour()] = true
	}
	hour := common.FromDBTimestamp(session.StartTime).Hour()
	for d := -hourTolerance; d <= hourTolerance; d++ {
		if usual[(hour+d+24)%24] {
			return Flag{}, false
		}
	}
	return Flag{
		Kind: KindOffHours,
		Signal: fmt.Sprintf("session started at %02d:00 UTC; this user's previous %d sessions started at %s UTC",
			hour, len(past), formatHours(usual)),
	}, true
}

// formatHours lists the hours as compact ranges: "08-11, 14".
func formatHours(set map[int]bool) string {
	hours := make([]int, 0, len(set))
	for h := range set {
		hours = append(hours, h)
	}
	sort.Ints(hours)
	var parts []string
	for i := 0; i < len(hours); {
		j := i
		for j+1 < len(hours) && hours[j+1] == hours[j]+1 {
			j++
		}
		if i == j {
			parts = append(parts, fmt.Sprintf("%02d", hours[i]))
		} else {
			parts = append(parts, fmt.Sprintf("%02d-%02d", hours[i], hours[j]))
		}
		i = j + 1
	}
	return strings.Join(parts, ", ")
}

// Record writes each flag as an audit entry on the session.
func Record(ctx context.Context, store Store, session *common.Session, machine *common.Machine, ip string, flags []Flag) error {
	for _, f := range flags {
		entry := common.NewAuditLog(session.UserID, ActionPrefix+f.Kind, "session:"+session.ID, ip, map[string]interface{}{
			"signal":      f.Signal,
			"machine_id":  session.MachineID,
			"machine":     machine.Name,
			"remote_user": session.RemoteUser,
			"session_id":  session.ID,
		})
		if err := store.CreateAuditLog(ctx, entry); err != nil {
			return fmt.Errorf("record %s anomaly for session %s: %w", f.Kind, session.ID, err)
		}
	}
	return nil
}

// Check detects and records anomalies for a session that just started.
// Failures are returned for logging; they must never stop the session.
func Check(ctx context.Context, store Store, session *common.Session, machine *common.Machine, ip string) ([]Flag, error) {
	flags, err := Detect(ctx, store, session, machine)
	if err != nil {
		return nil, err
	}
	return flags, Record(ctx, store, session, machine, ip, flags)
}
