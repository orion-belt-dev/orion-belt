package main

import (
	"github.com/orion-belt-dev/orion-belt/pkg/cliflags"
	"github.com/orion-belt-dev/orion-belt/pkg/sdk"
)

// nameIndex maps the user and machine IDs that appear as foreign keys in
// listings back to the names operators actually recognize.
//
// Sessions, permissions, requests, and audit entries all reference subjects by
// UUID. Printing raw UUIDs makes a table unreadable and printing truncated ones
// makes it useless, so listings resolve them once up front. Lookups are
// best-effort: a listing is still worth showing when the name query fails.
type nameIndex struct {
	users    map[string]string
	machines map[string]string
}

func newNameIndex(client *sdk.Client, wantUsers, wantMachines bool) *nameIndex {
	index := &nameIndex{users: map[string]string{}, machines: map[string]string{}}

	if wantUsers {
		if users, err := client.ListUsers(ctx()); err == nil {
			for _, user := range users {
				index.users[user.ID] = user.Username
			}
		}
	}
	if wantMachines {
		if machines, err := client.ListMachines(ctx()); err == nil {
			for _, machine := range machines {
				index.machines[machine.ID] = machine.Name
			}
		}
	}
	return index
}

// user renders a user ID as its username, falling back to a shortened ID when
// the name is unknown (a deleted account, or a failed lookup).
func (n *nameIndex) user(userID string) string {
	if userID == "" {
		return "-"
	}
	if name, ok := n.users[userID]; ok {
		return name
	}
	return cliflags.Short(userID)
}

// machine renders a machine ID as its name, on the same terms as user.
func (n *nameIndex) machine(machineID string) string {
	if machineID == "" {
		return "-"
	}
	if name, ok := n.machines[machineID]; ok {
		return name
	}
	return cliflags.Short(machineID)
}
