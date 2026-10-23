package anomaly

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/orion-belt-dev/orion-belt/pkg/common"
)

type fakeStore struct {
	sessions []*common.Session
	logs     []*common.AuditLog
	listErr  error
}

func (f *fakeStore) ListUserSessions(_ context.Context, userID string, limit, _ int) ([]*common.Session, error) {
	return f.sessions, f.listErr
}

func (f *fakeStore) CreateAuditLog(_ context.Context, l *common.AuditLog) error {
	f.logs = append(f.logs, l)
	return nil
}

// at builds a local-time instant on a fixed day, the way the gateway stamps
// sessions.
func at(hour int) time.Time { return time.Date(2026, 10, 20, hour, 30, 0, 0, time.Local) }

func session(id, machineID string, hour int) *common.Session {
	return &common.Session{ID: id, UserID: "u1", MachineID: machineID, RemoteUser: "root", StartTime: at(hour)}
}

var (
	sensitive = &common.Machine{ID: "db", Name: "db-01", Tags: map[string]string{common.SensitivityTagKey: "high"}}
	ordinary  = &common.Machine{ID: "web", Name: "web-01"}
)

func kinds(flags []Flag) string {
	var k []string
	for _, f := range flags {
		k = append(k, f.Kind)
	}
	return strings.Join(k, ",")
}

func TestFirstAccessToSensitiveTarget(t *testing.T) {
	current := session("now", "db", 10)
	store := &fakeStore{sessions: []*common.Session{current, session("old", "web", 9)}}
	flags, err := Detect(context.Background(), store, current, sensitive)
	if err != nil || kinds(flags) != KindFirstAccess {
		t.Fatalf("flags = %+v, %v", flags, err)
	}
	if !strings.Contains(flags[0].Signal, "db-01") || !strings.Contains(flags[0].Signal, "sensitivity high") {
		t.Errorf("signal = %q", flags[0].Signal)
	}

	store.sessions = append(store.sessions, session("earlier", "db", 9))
	if flags, _ := Detect(context.Background(), store, current, sensitive); len(flags) != 0 {
		t.Errorf("a repeat visit was flagged: %+v", flags)
	}
	if flags, _ := Detect(context.Background(), &fakeStore{}, session("x", "web", 10), ordinary); len(flags) != 0 {
		t.Errorf("an ordinary machine was flagged: %+v", flags)
	}
}

func TestOffHoursNeedsHistoryAndRespectsTolerance(t *testing.T) {
	var past []*common.Session
	for i := 0; i < minHistory; i++ {
		past = append(past, session(fmt.Sprint("p", i), "web", 9+i%3)) // 09-11
	}

	late := session("late", "web", 3)
	flags, _ := Detect(context.Background(), &fakeStore{sessions: past}, late, ordinary)
	if kinds(flags) != KindOffHours {
		t.Fatalf("flags = %+v", flags)
	}
	if !strings.Contains(flags[0].Signal, "previous 10 sessions") {
		t.Errorf("signal = %q", flags[0].Signal)
	}

	edge := session("edge", "web", 12) // one hour after the latest usual hour
	if flags, _ := Detect(context.Background(), &fakeStore{sessions: past}, edge, ordinary); len(flags) != 0 {
		t.Errorf("an hour of tolerance must not flag: %+v", flags)
	}
	if flags, _ := Detect(context.Background(), &fakeStore{sessions: past[:minHistory-1]}, late, ordinary); len(flags) != 0 {
		t.Errorf("too little history must not flag: %+v", flags)
	}
}

func TestCheckRecordsAuditEntries(t *testing.T) {
	current := session("s1", "db", 10)
	store := &fakeStore{}
	flags, err := Check(context.Background(), store, current, sensitive, "192.0.2.1")
	if err != nil || len(flags) != 1 || len(store.logs) != 1 {
		t.Fatalf("flags=%+v logs=%+v err=%v", flags, store.logs, err)
	}
	l := store.logs[0]
	if l.Action != ActionPrefix+KindFirstAccess || l.Resource != "session:s1" || l.Metadata["signal"] != flags[0].Signal || l.IPAddress != "192.0.2.1" {
		t.Errorf("audit entry = %+v", l)
	}

	if _, err := Check(context.Background(), &fakeStore{listErr: errors.New("down")}, current, sensitive, ""); err == nil {
		t.Error("a store failure must be reported")
	}
}

func TestFormatHours(t *testing.T) {
	got := formatHours(map[int]bool{8: true, 9: true, 10: true, 14: true, 23: true})
	if got != "08-10, 14, 23" {
		t.Errorf("formatHours = %q", got)
	}
}
