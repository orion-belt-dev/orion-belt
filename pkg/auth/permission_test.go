package auth

import (
	"context"
	"errors"
	"io"
	"testing"

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
