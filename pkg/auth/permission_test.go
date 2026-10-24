package auth

import (
	"context"
	"errors"
	"io"
	"testing"

	"github.com/orion-belt-dev/orion-belt/pkg/authz"
	"github.com/orion-belt-dev/orion-belt/pkg/common"
	"github.com/orion-belt-dev/orion-belt/pkg/database"
)

type permStore struct {
	database.Store
	user      *common.User
	hasRemote bool
	remoteErr error
}

func (s *permStore) GetUser(context.Context, string) (*common.User, error) { return s.user, nil }

func (s *permStore) HasPermissionWithRemoteUser(context.Context, string, string, string, string) (bool, error) {
	return s.hasRemote, s.remoteErr
}

type allowAllAuthorizer struct{}

func (allowAllAuthorizer) Check(context.Context, string, string, string) (bool, error) {
	return true, nil
}
func (allowAllAuthorizer) WriteGrant(context.Context, string, string, string) error  { return nil }
func (allowAllAuthorizer) DeleteGrant(context.Context, string, string, string) error { return nil }

func newPermService(store *permStore) *AuthService {
	svc := NewAuthService(store, common.NewLoggerTo(common.FATAL, io.Discard))
	svc.SetAuthorizer(allowAllAuthorizer{})
	return svc
}

// OpenFGA answers only "may this user reach the machine"; the remote Unix
// account is still enforced locally. If that local check can't run, access
// must be denied rather than granted as any account (including root).
func TestCheckPermissionWithRemoteUserFailsClosedOnStoreError(t *testing.T) {
	store := &permStore{
		user:      common.NewUser("alice", "a@example.com", "", false),
		remoteErr: errors.New("db unavailable"),
	}
	err := newPermService(store).CheckPermissionWithRemoteUser(context.Background(), store.user.ID, "m1", "ssh", "root")
	if err == nil {
		t.Fatal("expected denial when the remote-user check errors, got access")
	}
}

func TestCheckPermissionWithRemoteUserOpenFGA(t *testing.T) {
	cases := []struct {
		name      string
		hasRemote bool
		wantErr   bool
	}{
		{"remote user granted", true, false},
		{"remote user not granted", false, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			store := &permStore{user: common.NewUser("alice", "a@example.com", "", false), hasRemote: tc.hasRemote}
			err := newPermService(store).CheckPermissionWithRemoteUser(context.Background(), store.user.ID, "m1", "ssh", "root")
			if (err != nil) != tc.wantErr {
				t.Fatalf("err = %v, wantErr %v", err, tc.wantErr)
			}
		})
	}
}

// tunnelStore answers HasPermission from a set of granted access types.
type tunnelStore struct {
	permStore
	granted map[string]bool
	err     error
}

func (s *tunnelStore) HasPermission(_ context.Context, _, _, accessType string) (bool, error) {
	return s.granted[accessType], s.err
}

type errAuthorizer struct{ fakeAuthorizer }

func (errAuthorizer) Check(context.Context, string, string, string) (bool, error) {
	return false, errors.New("openfga down")
}

func TestCheckTunnelPermission(t *testing.T) {
	alice := common.NewUser("alice", "a@example.com", "", false)
	admin := common.NewUser("root", "r@example.com", "", true)
	cases := []struct {
		name       string
		user       *common.User
		authorizer authz.Authorizer
		granted    map[string]bool
		storeErr   error
		protocol   string
		wantErr    bool
	}{
		{name: "local rdp grant", user: alice, granted: map[string]bool{"rdp": true}, protocol: "rdp"},
		{name: "no grant", user: alice, protocol: "rdp", wantErr: true},
		{name: "admin needs no grant", user: admin, protocol: "vnc"},
		{name: "not a tunnel protocol", user: admin, protocol: "ssh", wantErr: true},
		// OpenFGA allowing the machine is not enough: its model has no access
		// types, so the local grant must still be for this protocol.
		{name: "openfga allows, no local grant", user: alice, authorizer: allowAllAuthorizer{}, protocol: "rdp", wantErr: true},
		{name: "openfga allows, local grant", user: alice, authorizer: allowAllAuthorizer{}, granted: map[string]bool{"rdp": true}, protocol: "rdp"},
		{name: "openfga denies", user: alice, authorizer: fakeAuthorizer{allow: false}, granted: map[string]bool{"rdp": true}, protocol: "rdp", wantErr: true},
		{name: "openfga down falls back", user: alice, authorizer: errAuthorizer{}, granted: map[string]bool{"vnc": true}, protocol: "vnc"},
		{name: "store error", user: alice, storeErr: errors.New("db down"), protocol: "rdp", wantErr: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			store := &tunnelStore{permStore: permStore{user: tc.user}, granted: tc.granted, err: tc.storeErr}
			svc := NewAuthService(store, common.NewLoggerTo(common.FATAL, io.Discard))
			if tc.authorizer != nil {
				svc.SetAuthorizer(tc.authorizer)
			}
			err := svc.CheckTunnelPermission(context.Background(), tc.user.ID, "m1", tc.protocol)
			if (err != nil) != tc.wantErr {
				t.Fatalf("err = %v, wantErr %v", err, tc.wantErr)
			}
		})
	}
}

func TestCheckTunnelPermissionUnknownUser(t *testing.T) {
	store := &unknownUserStore{}
	svc := NewAuthService(store, common.NewLoggerTo(common.FATAL, io.Discard))
	if err := svc.CheckTunnelPermission(context.Background(), "ghost", "m1", "rdp"); err == nil {
		t.Fatal("unknown user was allowed")
	}
}

type unknownUserStore struct{ database.Store }

func (unknownUserStore) GetUser(context.Context, string) (*common.User, error) {
	return nil, database.ErrNotFound
}
