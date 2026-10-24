package auth

import (
	"context"
	"fmt"
	"sort"
	"time"

	"github.com/orion-belt-dev/orion-belt/pkg/common"
)

// EffectiveAccess is one thing a user can actually do on a machine, after
// roles and grants are resolved, and what allows it.
type EffectiveAccess struct {
	UserID      string `json:"user_id"`
	Username    string `json:"username"`
	MachineID   string `json:"machine_id"`
	MachineName string `json:"machine_name"`
	// AccessType is ssh, scp, rdp, vnc or k8s; "*" for every type, which only
	// a role gives. A "both" grant appears as an ssh and an scp row.
	AccessType string `json:"access_type"`
	// RemoteUsers are the accounts allowed (scopes for k8s). Empty for rdp
	// and vnc; nil with AccessType "*" means any account.
	RemoteUsers []string `json:"remote_users"`
	// Source is role:<role> or permission:<id>.
	Source    string     `json:"source"`
	ExpiresAt *time.Time `json:"expires_at,omitempty"`
}

// effectiveListLimit bounds the users and machines read to resolve access.
const effectiveListLimit = 10000

// EffectiveForUser lists what userID can do on every machine.
func (a *AuthService) EffectiveForUser(ctx context.Context, userID string) ([]EffectiveAccess, error) {
	user, err := a.store.GetUser(ctx, userID)
	if err != nil {
		return nil, fmt.Errorf("get user: %w", err)
	}
	machines, err := a.store.ListMachines(ctx, effectiveListLimit, 0)
	if err != nil {
		return nil, fmt.Errorf("list machines: %w", err)
	}
	names := map[string]string{}
	for _, m := range machines {
		names[m.ID] = m.Name
	}

	var out []EffectiveAccess
	if privilegedAccess(user) {
		for _, m := range machines {
			out = append(out, roleAccess(user, m.ID, m.Name))
		}
	}
	perms, err := a.store.ListUserPermissions(ctx, userID)
	if err != nil {
		return nil, fmt.Errorf("list grants: %w", err)
	}
	for _, p := range perms {
		out = append(out, grantAccess(p, user.Username, names[p.MachineID])...)
	}
	sortEffective(out)
	return out, nil
}

// EffectiveForMachine lists what every user can do on machineID.
func (a *AuthService) EffectiveForMachine(ctx context.Context, machineID string) ([]EffectiveAccess, error) {
	machine, err := a.store.GetMachine(ctx, machineID)
	if err != nil {
		return nil, fmt.Errorf("get machine: %w", err)
	}
	users, err := a.store.ListUsers(ctx, effectiveListLimit, 0)
	if err != nil {
		return nil, fmt.Errorf("list users: %w", err)
	}
	names := map[string]string{}
	var out []EffectiveAccess
	for _, u := range users {
		names[u.ID] = u.Username
		if privilegedAccess(u) {
			out = append(out, roleAccess(u, machine.ID, machine.Name))
		}
	}
	perms, err := a.store.ListMachinePermissions(ctx, machineID)
	if err != nil {
		return nil, fmt.Errorf("list grants: %w", err)
	}
	for _, p := range perms {
		out = append(out, grantAccess(p, names[p.UserID], machine.Name)...)
	}
	sortEffective(out)
	return out, nil
}

func roleAccess(u *common.User, machineID, machineName string) EffectiveAccess {
	return EffectiveAccess{
		UserID: u.ID, Username: u.Username, MachineID: machineID, MachineName: machineName,
		AccessType: "*", Source: "role:" + u.EffectiveRole(),
	}
}

// grantAccess expands a grant into the access it gives: "both" (and the
// empty default) is ssh and scp.
func grantAccess(p *common.Permission, username, machineName string) []EffectiveAccess {
	types := []string{p.AccessType}
	if p.AccessType == common.AccessTypeBoth || p.AccessType == "" {
		types = []string{common.AccessTypeSSH, common.AccessTypeSCP}
	}
	remote := p.RemoteUsers
	if remote == nil {
		remote = []string{}
	}
	out := make([]EffectiveAccess, 0, len(types))
	for _, t := range types {
		out = append(out, EffectiveAccess{
			UserID: p.UserID, Username: username, MachineID: p.MachineID, MachineName: machineName,
			AccessType: t, RemoteUsers: remote, Source: "permission:" + p.ID, ExpiresAt: p.ExpiresAt,
		})
	}
	return out
}

func sortEffective(rows []EffectiveAccess) {
	sort.SliceStable(rows, func(i, j int) bool {
		a, b := rows[i], rows[j]
		if a.Username != b.Username {
			return a.Username < b.Username
		}
		if a.MachineName != b.MachineName {
			return a.MachineName < b.MachineName
		}
		return a.AccessType < b.AccessType
	})
}
