package main

import (
	"context"
	"os"
	"os/user"

	"github.com/orion-belt-dev/orion-belt/pkg/common"
	"github.com/orion-belt-dev/orion-belt/pkg/database"
)

// auditLocal records a change made with the server CLI. These commands write
// to the database directly, past the API that audits everything else, so
// without this an admin could be created or re-keyed with no trace. The
// entry names the host account that ran the command.
func auditLocal(ctx context.Context, store database.Store, actorID, action, resource string, meta map[string]interface{}) {
	if meta == nil {
		meta = map[string]interface{}{}
	}
	meta["via"] = "server-cli"
	if u, err := user.Current(); err == nil {
		meta["os_user"] = u.Username
	}
	if host, err := os.Hostname(); err == nil {
		meta["host"] = host
	}
	if err := store.CreateAuditLog(ctx, common.NewAuditLog(actorID, action, resource, "", meta)); err != nil {
		getLogger().Error("AUDIT WRITE FAILED for %s on %s: %v (record this change manually)", action, resource, err)
	}
}
