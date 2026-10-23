package main

import (
	"context"
	"testing"

	"github.com/orion-belt-dev/orion-belt/pkg/common"
	"github.com/orion-belt-dev/orion-belt/pkg/database"
)

type auditStore struct {
	database.Store
	logs []*common.AuditLog
}

func (s *auditStore) CreateAuditLog(_ context.Context, l *common.AuditLog) error {
	s.logs = append(s.logs, l)
	return nil
}

// A server-CLI change is attributed to the CLI and the host account, never
// left unrecorded.
func TestAuditLocalMarksServerCLIChanges(t *testing.T) {
	store := &auditStore{}
	auditLocal(context.Background(), store, "", "user.create", "user:u1", map[string]interface{}{"username": "breakglass"})
	if len(store.logs) != 1 {
		t.Fatalf("logs = %+v", store.logs)
	}
	l := store.logs[0]
	if l.Action != "user.create" || l.Resource != "user:u1" || l.UserID != "" {
		t.Errorf("entry = %+v", l)
	}
	if l.Metadata["via"] != "server-cli" || l.Metadata["username"] != "breakglass" || l.Metadata["host"] == nil {
		t.Errorf("metadata = %+v", l.Metadata)
	}
}
