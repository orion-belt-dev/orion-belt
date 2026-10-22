package database

import (
	"context"
	"testing"

	"github.com/orion-belt-dev/orion-belt/pkg/common"
)

// Deleting a user nulls the rows that only attribute an action to them
// (permissions.granted_by, audit_logs.user_id). Those rows must stay
// readable afterwards; a NULL scanned into a string field used to fail every
// listing that touched them.
func TestDeletedActor_RowsStayReadable(t *testing.T) {
	pg := openTestPostgres(t)
	ctx := context.Background()

	admin := common.NewUser("admin", "admin@example.com", "", true)
	alice := common.NewUser("alice", "alice@example.com", "", false)
	for _, u := range []*common.User{admin, alice} {
		if err := pg.CreateUser(ctx, u); err != nil {
			t.Fatal(err)
		}
	}
	m := common.NewMachine("web-01", "10.0.0.1", 22, nil)
	if err := pg.CreateMachine(ctx, m); err != nil {
		t.Fatal(err)
	}
	perm := common.NewPermission(alice.ID, m.ID, "ssh", []string{"deploy"}, admin.ID, nil)
	if err := pg.CreatePermission(ctx, perm); err != nil {
		t.Fatal(err)
	}
	if err := pg.CreateAuditLog(ctx, common.NewAuditLog(admin.ID, "permission.grant", "permission", "10.0.0.9", nil)); err != nil {
		t.Fatal(err)
	}

	if err := pg.DeleteUser(ctx, admin.ID); err != nil {
		t.Fatalf("DeleteUser: %v", err)
	}

	check := func(name string, got []*common.Permission, err error) {
		t.Helper()
		if err != nil {
			t.Fatalf("%s after deleting the grantor: %v", name, err)
		}
		if len(got) != 1 || got[0].ID != perm.ID || got[0].GrantedBy != "" {
			t.Errorf("%s = %+v, want alice's grant with no grantor", name, got)
		}
	}
	byUser, err := pg.ListUserPermissions(ctx, alice.ID)
	check("ListUserPermissions", byUser, err)
	byMachine, err := pg.ListMachinePermissions(ctx, m.ID)
	check("ListMachinePermissions", byMachine, err)
	all, err := pg.ListAllPermissions(ctx, 0, 0)
	check("ListAllPermissions", all, err)

	one, err := pg.GetPermission(ctx, perm.ID)
	check("GetPermission", []*common.Permission{one}, err)
	found, err := pg.FindActivePermission(ctx, alice.ID, m.ID, "ssh")
	check("FindActivePermission", []*common.Permission{found}, err)

	logs, err := pg.ListAuditLogs(ctx, 10, 0, nil)
	if err != nil {
		t.Fatalf("ListAuditLogs after deleting the actor: %v", err)
	}
	if len(logs) != 1 || logs[0].UserID != "" || logs[0].Action != "permission.grant" {
		t.Errorf("ListAuditLogs = %+v, want the grant entry with no user", logs)
	}
}
