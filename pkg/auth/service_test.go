package auth

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"

	"github.com/orion-belt-dev/orion-belt/pkg/common"
	"github.com/orion-belt-dev/orion-belt/pkg/database"
)

var errDB = errors.New("db unavailable")

// memStore is an in-memory slice of database.Store covering what AuthService
// uses. fail[method] makes that method return the given error.
type memStore struct {
	database.Store

	mu          sync.Mutex
	fail        map[string]error
	users       map[string]*common.User
	sshKeys     map[string][]*common.SSHKey
	perms       map[string]*common.Permission
	requests    map[string]*common.AccessRequest
	apiKeys     map[string]*common.APIKey
	sessions    map[string]*common.HTTPSession
	hasPerm     bool
	hasRemote   bool
	purgedCount int
}

func newMemStore() *memStore {
	return &memStore{
		fail:     map[string]error{},
		users:    map[string]*common.User{},
		sshKeys:  map[string][]*common.SSHKey{},
		perms:    map[string]*common.Permission{},
		requests: map[string]*common.AccessRequest{},
		apiKeys:  map[string]*common.APIKey{},
		sessions: map[string]*common.HTTPSession{},
	}
}

func (s *memStore) GetUser(_ context.Context, id string) (*common.User, error) {
	if err := s.fail["GetUser"]; err != nil {
		return nil, err
	}
	if u, ok := s.users[id]; ok {
		return u, nil
	}
	return nil, database.ErrNotFound
}

func (s *memStore) GetUserByUsername(_ context.Context, name string) (*common.User, error) {
	for _, u := range s.users {
		if u.Username == name {
			return u, nil
		}
	}
	return nil, database.ErrNotFound
}

func (s *memStore) ListUserSSHKeys(_ context.Context, userID string) ([]*common.SSHKey, error) {
	if err := s.fail["ListUserSSHKeys"]; err != nil {
		return nil, err
	}
	return s.sshKeys[userID], nil
}

func (s *memStore) HasPermission(context.Context, string, string, string) (bool, error) {
	return s.hasPerm, s.fail["HasPermission"]
}

func (s *memStore) HasPermissionWithRemoteUser(context.Context, string, string, string, string) (bool, error) {
	return s.hasRemote, s.fail["HasPermissionWithRemoteUser"]
}

func (s *memStore) FindActivePermission(_ context.Context, userID, machineID, accessType string) (*common.Permission, error) {
	for _, p := range s.perms {
		if p.UserID == userID && p.MachineID == machineID && p.AccessType == accessType {
			return p, nil
		}
	}
	return nil, database.ErrNotFound
}

func (s *memStore) CreatePermission(_ context.Context, p *common.Permission) error {
	if err := s.fail["CreatePermission"]; err != nil {
		return err
	}
	s.perms[p.ID] = p
	return nil
}

func (s *memStore) UpdatePermission(_ context.Context, p *common.Permission) error {
	if err := s.fail["UpdatePermission"]; err != nil {
		return err
	}
	s.perms[p.ID] = p
	return nil
}

func (s *memStore) GetPermission(_ context.Context, id string) (*common.Permission, error) {
	if p, ok := s.perms[id]; ok {
		return p, nil
	}
	return nil, database.ErrNotFound
}

func (s *memStore) DeletePermission(_ context.Context, id string) error {
	if err := s.fail["DeletePermission"]; err != nil {
		return err
	}
	delete(s.perms, id)
	return nil
}

func (s *memStore) CreateAccessRequest(_ context.Context, r *common.AccessRequest) error {
	if err := s.fail["CreateAccessRequest"]; err != nil {
		return err
	}
	s.requests[r.ID] = r
	return nil
}

func (s *memStore) GetAccessRequest(_ context.Context, id string) (*common.AccessRequest, error) {
	if r, ok := s.requests[id]; ok {
		return r, nil
	}
	return nil, database.ErrNotFound
}

func (s *memStore) UpdateAccessRequest(_ context.Context, r *common.AccessRequest) error {
	if err := s.fail["UpdateAccessRequest"]; err != nil {
		return err
	}
	s.requests[r.ID] = r
	return nil
}

func (s *memStore) ListPendingAccessRequests(context.Context) ([]*common.AccessRequest, error) {
	var out []*common.AccessRequest
	for _, r := range s.requests {
		if r.Status == "pending" {
			out = append(out, r)
		}
	}
	return out, nil
}

func (s *memStore) CreateAPIKey(_ context.Context, k *common.APIKey) error {
	if err := s.fail["CreateAPIKey"]; err != nil {
		return err
	}
	s.apiKeys[k.ID] = k
	return nil
}

func (s *memStore) RevokeAPIKey(_ context.Context, id string) error {
	if err := s.fail["RevokeAPIKey"]; err != nil {
		return err
	}
	now := time.Now()
	s.apiKeys[id].RevokedAt = &now
	return nil
}

func (s *memStore) ListUserAPIKeys(_ context.Context, userID string) ([]*common.APIKey, error) {
	if err := s.fail["ListUserAPIKeys"]; err != nil {
		return nil, err
	}
	var out []*common.APIKey
	for _, k := range s.apiKeys {
		if k.UserID == userID {
			out = append(out, k)
		}
	}
	return out, nil
}

func (s *memStore) CreateHTTPSession(_ context.Context, sess *common.HTTPSession) error {
	if err := s.fail["CreateHTTPSession"]; err != nil {
		return err
	}
	s.sessions[sess.ID] = sess
	return nil
}

func (s *memStore) DeleteHTTPSession(_ context.Context, id string) error {
	if err := s.fail["DeleteHTTPSession"]; err != nil {
		return err
	}
	delete(s.sessions, id)
	return nil
}

func (s *memStore) DeleteExpiredHTTPSessions(context.Context) error {
	if err := s.fail["DeleteExpiredHTTPSessions"]; err != nil {
		return err
	}
	s.purgedCount++
	return nil
}

// recordingAuthorizer is a configurable OpenFGA stand-in.
type recordingAuthorizer struct {
	allowed  bool
	checkErr error
	writeErr error
	writes   []string
	deletes  []string
}

func (r *recordingAuthorizer) Check(context.Context, string, string, string) (bool, error) {
	return r.allowed, r.checkErr
}

func (r *recordingAuthorizer) WriteGrant(_ context.Context, u, m, a string) error {
	r.writes = append(r.writes, u+"/"+m+"/"+a)
	return r.writeErr
}

func (r *recordingAuthorizer) DeleteGrant(_ context.Context, u, m, a string) error {
	r.deletes = append(r.deletes, u+"/"+m+"/"+a)
	return r.writeErr
}

func newService(t *testing.T) (*AuthService, *memStore, *bytes.Buffer) {
	t.Helper()
	var logs bytes.Buffer
	store := newMemStore()
	return NewAuthService(store, common.NewLoggerTo(common.DEBUG, &logs)), store, &logs
}

func addUser(s *memStore, name, role string, isAdmin bool, pubKey string) *common.User {
	u := common.NewUser(name, name+"@example.com", pubKey, isAdmin)
	u.Role = role
	s.users[u.ID] = u
	return u
}

func newKeyPair(t *testing.T) (ssh.PublicKey, string) {
	t.Helper()
	_, pubLine, err := GenerateSSHKeyPair()
	if err != nil {
		t.Fatal(err)
	}
	pub, _, _, _, err := ssh.ParseAuthorizedKey([]byte(pubLine))
	if err != nil {
		t.Fatal(err)
	}
	return pub, pubLine
}

// --- authentication ------------------------------------------------------------

func TestAuthenticateUser(t *testing.T) {
	svc, store, _ := newService(t)
	primary, primaryLine := newKeyPair(t)
	extra, extraLine := newKeyPair(t)
	stranger, _ := newKeyPair(t)
	alice := addUser(store, "alice", common.RoleUser, false, primaryLine)
	store.sshKeys[alice.ID] = []*common.SSHKey{
		{Name: "broken", PublicKey: "not a key"},
		{Name: "laptop", PublicKey: extraLine},
	}
	ctx := context.Background()

	cases := []struct {
		name     string
		username string
		key      ssh.PublicKey
		wantOK   bool
	}{
		{"primary key", "alice", primary, true},
		{"additional key", "alice", extra, true},
		{"agentless target suffix is stripped", "alice+root%web-01", primary, true},
		{"unknown key", "alice", stranger, false},
		{"unknown user", "mallory", primary, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			u, err := svc.AuthenticateUser(ctx, tc.username, tc.key)
			if tc.wantOK {
				if err != nil || u.ID != alice.ID {
					t.Errorf("AuthenticateUser = %v, %v; want alice", u, err)
				}
				return
			}
			if err == nil || err.Error() != "authentication failed" {
				t.Errorf("AuthenticateUser error = %v, want a generic failure", err)
			}
		})
	}
}

func TestAuthenticateUser_KeyListErrorStillRejects(t *testing.T) {
	svc, store, _ := newService(t)
	_, line := newKeyPair(t)
	other, _ := newKeyPair(t)
	addUser(store, "alice", common.RoleUser, false, line)
	store.fail["ListUserSSHKeys"] = errDB

	if _, err := svc.AuthenticateUser(context.Background(), "alice", other); err == nil {
		t.Fatal("AuthenticateUser succeeded with an unmatched key while the key list failed")
	}
}

// --- permission checks -----------------------------------------------------------

func TestPrivilegedAccess(t *testing.T) {
	cases := []struct {
		user *common.User
		want bool
	}{
		{nil, false},
		{&common.User{IsAdmin: true, Role: common.RoleUser}, true},
		{&common.User{Role: common.RoleAdmin}, true},
		{&common.User{Role: common.RoleOperator}, true},
		{&common.User{Role: common.RoleAuditor}, false},
		{&common.User{Role: common.RoleUser}, false},
	}
	for _, tc := range cases {
		if got := privilegedAccess(tc.user); got != tc.want {
			t.Errorf("privilegedAccess(%+v) = %v, want %v", tc.user, got, tc.want)
		}
	}
}

func TestCheckPermission(t *testing.T) {
	cases := []struct {
		name       string
		role       string
		authz      *recordingAuthorizer
		hasPerm    bool
		permErr    error
		wantErr    error
		wantAnyErr bool
	}{
		{name: "operator bypasses checks", role: common.RoleOperator},
		{name: "openfga allows", role: common.RoleUser, authz: &recordingAuthorizer{allowed: true}},
		{name: "openfga denies", role: common.RoleUser, authz: &recordingAuthorizer{}, hasPerm: true, wantErr: database.ErrPermissionDenied},
		{name: "openfga error falls back to local grant", role: common.RoleUser, authz: &recordingAuthorizer{checkErr: errDB}, hasPerm: true},
		{name: "local grant", role: common.RoleUser, hasPerm: true},
		{name: "no local grant", role: common.RoleUser, wantErr: database.ErrPermissionDenied},
		{name: "local check error", role: common.RoleUser, permErr: errDB, wantErr: errDB},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			svc, store, _ := newService(t)
			u := addUser(store, "u", tc.role, false, "")
			store.hasPerm = tc.hasPerm
			if tc.permErr != nil {
				store.fail["HasPermission"] = tc.permErr
			}
			if tc.authz != nil {
				svc.SetAuthorizer(tc.authz)
			}
			err := svc.CheckPermission(context.Background(), u.ID, "m1", "ssh")
			if tc.wantErr == nil && err != nil {
				t.Fatalf("CheckPermission = %v, want nil", err)
			}
			if tc.wantErr != nil && !errors.Is(err, tc.wantErr) {
				t.Fatalf("CheckPermission = %v, want %v", err, tc.wantErr)
			}
		})
	}
}

func TestCheckPermission_UnknownUser(t *testing.T) {
	svc, _, _ := newService(t)
	if err := svc.CheckPermission(context.Background(), "ghost", "m", "ssh"); !errors.Is(err, database.ErrNotFound) {
		t.Errorf("CheckPermission = %v, want ErrNotFound", err)
	}
	if err := svc.CheckPermissionWithRemoteUser(context.Background(), "ghost", "m", "ssh", "root"); !errors.Is(err, database.ErrNotFound) {
		t.Errorf("CheckPermissionWithRemoteUser = %v, want ErrNotFound", err)
	}
}

func TestCheckPermissionWithRemoteUser(t *testing.T) {
	cases := []struct {
		name      string
		role      string
		authz     *recordingAuthorizer
		hasRemote bool
		remoteErr error
		wantErr   bool
	}{
		{name: "admin bypasses checks", role: common.RoleAdmin},
		{name: "openfga denies", role: common.RoleUser, authz: &recordingAuthorizer{}, hasRemote: true, wantErr: true},
		{name: "openfga error falls back to local", role: common.RoleUser, authz: &recordingAuthorizer{checkErr: errDB}, hasRemote: true},
		{name: "local grant for remote user", role: common.RoleUser, hasRemote: true},
		{name: "remote user not granted", role: common.RoleUser, wantErr: true},
		{name: "local check error", role: common.RoleUser, remoteErr: errDB, wantErr: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			svc, store, _ := newService(t)
			u := addUser(store, "u", tc.role, false, "")
			store.hasRemote = tc.hasRemote
			if tc.remoteErr != nil {
				store.fail["HasPermissionWithRemoteUser"] = tc.remoteErr
			}
			if tc.authz != nil {
				svc.SetAuthorizer(tc.authz)
			}
			err := svc.CheckPermissionWithRemoteUser(context.Background(), u.ID, "m1", "ssh", "root")
			if (err != nil) != tc.wantErr {
				t.Fatalf("CheckPermissionWithRemoteUser = %v, wantErr %v", err, tc.wantErr)
			}
		})
	}
}

// --- grants ----------------------------------------------------------------------

func TestGrantPermission_CreatesThenUpdatesInPlace(t *testing.T) {
	svc, store, _ := newService(t)
	authz := &recordingAuthorizer{}
	svc.SetAuthorizer(authz)
	ctx := context.Background()

	ttl := time.Hour
	if err := svc.GrantPermission(ctx, "u", "m", "ssh", []string{"deploy"}, "admin", &ttl); err != nil {
		t.Fatalf("GrantPermission: %v", err)
	}
	if len(store.perms) != 1 || len(authz.writes) != 1 {
		t.Fatalf("perms=%d tuple writes=%d, want 1 and 1", len(store.perms), len(authz.writes))
	}
	var first *common.Permission
	for _, p := range store.perms {
		first = p
	}
	if first.ExpiresAt == nil || time.Until(*first.ExpiresAt) > time.Hour {
		t.Errorf("ExpiresAt = %v, want about an hour from now", first.ExpiresAt)
	}

	if err := svc.GrantPermission(ctx, "u", "m", "ssh", []string{"root"}, "admin2", nil); err != nil {
		t.Fatalf("second GrantPermission: %v", err)
	}
	if len(store.perms) != 1 {
		t.Fatalf("second grant created a new row; perms=%d, want upsert", len(store.perms))
	}
	if first.RemoteUsers[0] != "root" || first.GrantedBy != "admin2" || first.ExpiresAt != nil {
		t.Errorf("upserted grant = %+v, want root, admin2, no expiry", first)
	}
}

func TestGrantPermission_Failures(t *testing.T) {
	ctx := context.Background()

	t.Run("create fails", func(t *testing.T) {
		svc, store, _ := newService(t)
		store.fail["CreatePermission"] = errDB
		if err := svc.GrantPermission(ctx, "u", "m", "ssh", nil, "a", nil); !errors.Is(err, errDB) {
			t.Errorf("GrantPermission = %v, want the store error", err)
		}
	})
	t.Run("update fails", func(t *testing.T) {
		svc, store, _ := newService(t)
		_ = svc.GrantPermission(ctx, "u", "m", "ssh", nil, "a", nil)
		store.fail["UpdatePermission"] = errDB
		if err := svc.GrantPermission(ctx, "u", "m", "ssh", nil, "a", nil); !errors.Is(err, errDB) {
			t.Errorf("GrantPermission = %v, want the store error", err)
		}
	})
	t.Run("tuple write failure is logged, not fatal", func(t *testing.T) {
		svc, _, logs := newService(t)
		svc.SetAuthorizer(&recordingAuthorizer{writeErr: errDB})
		if err := svc.GrantPermission(ctx, "u", "m", "ssh", nil, "a", nil); err != nil {
			t.Fatalf("GrantPermission = %v, want success", err)
		}
		if !strings.Contains(logs.String(), "Failed to write OpenFGA tuple") {
			t.Errorf("log = %q, want the tuple failure", logs.String())
		}
	})
}

func TestRevokePermission(t *testing.T) {
	ctx := context.Background()
	svc, store, logs := newService(t)
	authz := &recordingAuthorizer{writeErr: errDB}
	svc.SetAuthorizer(authz)
	_ = svc.GrantPermission(ctx, "u", "m", "ssh", nil, "a", nil)
	var id string
	for k := range store.perms {
		id = k
	}

	if err := svc.RevokePermission(ctx, id); err != nil {
		t.Fatalf("RevokePermission: %v", err)
	}
	if len(store.perms) != 0 || len(authz.deletes) != 1 || authz.deletes[0] != "u/m/ssh" {
		t.Errorf("perms=%d deletes=%v, want row removed and tuple deleted", len(store.perms), authz.deletes)
	}
	if !strings.Contains(logs.String(), "Failed to delete OpenFGA tuple") {
		t.Errorf("log = %q, want the tuple delete failure logged", logs.String())
	}

	if err := svc.RevokePermission(ctx, "missing"); !errors.Is(err, database.ErrNotFound) {
		t.Errorf("RevokePermission(missing) = %v, want ErrNotFound", err)
	}
	_ = svc.GrantPermission(ctx, "u", "m", "ssh", nil, "a", nil)
	for k := range store.perms {
		id = k
	}
	store.fail["DeletePermission"] = errDB
	if err := svc.RevokePermission(ctx, id); !errors.Is(err, errDB) {
		t.Errorf("RevokePermission with failing delete = %v, want the store error", err)
	}
}

// --- access requests ---------------------------------------------------------------

func TestAccessRequestLifecycle(t *testing.T) {
	ctx := context.Background()
	svc, store, _ := newService(t)

	req, err := svc.RequestTemporaryAccess(ctx, "u", "m", []string{"deploy"}, "deploy fix", 600)
	if err != nil {
		t.Fatalf("RequestTemporaryAccess: %v", err)
	}
	pending, _ := svc.GetPendingRequests(ctx)
	if len(pending) != 1 {
		t.Fatalf("pending = %d, want 1", len(pending))
	}

	if err := svc.ApproveAccessRequest(ctx, req.ID, "admin"); err != nil {
		t.Fatalf("ApproveAccessRequest: %v", err)
	}
	if req.Status != "approved" || *req.ReviewedBy != "admin" || req.ExpiresAt == nil {
		t.Errorf("approved request = %+v", req)
	}
	perm, err := store.FindActivePermission(ctx, "u", "m", "both")
	if err != nil || perm.ExpiresAt == nil || perm.RemoteUsers[0] != "deploy" {
		t.Fatalf("granted permission = %+v, %v; want a 'both' grant for deploy with expiry", perm, err)
	}

	if err := svc.ApproveAccessRequest(ctx, req.ID, "admin"); err == nil {
		t.Error("approving an already approved request succeeded")
	}
	if err := svc.RejectAccessRequest(ctx, req.ID, "admin"); err == nil {
		t.Error("rejecting an already approved request succeeded")
	}
}

func TestApproveAccessRequestWithTTL_ZeroMeansUnlimited(t *testing.T) {
	ctx := context.Background()
	svc, store, _ := newService(t)
	req, _ := svc.RequestTemporaryAccess(ctx, "u", "m", nil, "", 600)
	req.AccessType = "ssh"

	zero := 0
	if err := svc.ApproveAccessRequestWithTTL(ctx, req.ID, "admin", &zero); err != nil {
		t.Fatalf("ApproveAccessRequestWithTTL: %v", err)
	}
	if req.Duration != 0 || req.ExpiresAt != nil {
		t.Errorf("request duration=%d expires=%v, want unlimited", req.Duration, req.ExpiresAt)
	}
	perm, err := store.FindActivePermission(ctx, "u", "m", "ssh")
	if err != nil || perm.ExpiresAt != nil {
		t.Errorf("permission = %+v, %v; want an ssh grant without expiry", perm, err)
	}
}

func TestApproveAccessRequest_Failures(t *testing.T) {
	ctx := context.Background()

	t.Run("unknown request", func(t *testing.T) {
		svc, _, _ := newService(t)
		if err := svc.ApproveAccessRequest(ctx, "missing", "a"); !errors.Is(err, database.ErrNotFound) {
			t.Errorf("Approve = %v, want ErrNotFound", err)
		}
	})
	t.Run("update fails", func(t *testing.T) {
		svc, store, _ := newService(t)
		req, _ := svc.RequestTemporaryAccess(ctx, "u", "m", nil, "", 60)
		store.fail["UpdateAccessRequest"] = errDB
		if err := svc.ApproveAccessRequest(ctx, req.ID, "a"); !errors.Is(err, errDB) {
			t.Errorf("Approve = %v, want the store error", err)
		}
	})
	t.Run("grant fails", func(t *testing.T) {
		svc, store, _ := newService(t)
		req, _ := svc.RequestTemporaryAccess(ctx, "u", "m", nil, "", 60)
		store.fail["CreatePermission"] = errDB
		if err := svc.ApproveAccessRequest(ctx, req.ID, "a"); err == nil || !strings.Contains(err.Error(), "failed to grant permission") {
			t.Errorf("Approve = %v, want a grant failure", err)
		}
	})
}

func TestRejectAccessRequest(t *testing.T) {
	ctx := context.Background()
	svc, store, _ := newService(t)
	req, _ := svc.RequestTemporaryAccess(ctx, "u", "m", nil, "", 60)

	if err := svc.RejectAccessRequest(ctx, req.ID, "admin"); err != nil {
		t.Fatalf("Reject: %v", err)
	}
	if req.Status != "rejected" || *req.ReviewedBy != "admin" || req.ReviewedAt == nil {
		t.Errorf("rejected request = %+v", req)
	}
	if len(store.perms) != 0 {
		t.Error("rejecting a request granted a permission")
	}
	if err := svc.RejectAccessRequest(ctx, "missing", "a"); !errors.Is(err, database.ErrNotFound) {
		t.Errorf("Reject(missing) = %v, want ErrNotFound", err)
	}

	req2, _ := svc.RequestTemporaryAccess(ctx, "u", "m", nil, "", 60)
	store.fail["UpdateAccessRequest"] = errDB
	if err := svc.RejectAccessRequest(ctx, req2.ID, "a"); !errors.Is(err, errDB) {
		t.Errorf("Reject with failing update = %v, want the store error", err)
	}
}

func TestRequestTemporaryAccess_StoreError(t *testing.T) {
	svc, store, _ := newService(t)
	store.fail["CreateAccessRequest"] = errDB
	if _, err := svc.RequestTemporaryAccess(context.Background(), "u", "m", nil, "", 60); !errors.Is(err, errDB) {
		t.Errorf("RequestTemporaryAccess = %v, want the store error", err)
	}
}

// --- API keys and sessions -----------------------------------------------------------

func TestAPIKeyLifecycle(t *testing.T) {
	ctx := context.Background()
	svc, store, _ := newService(t)

	key, raw, err := svc.GenerateAPIKey(ctx, "u", "ci", nil)
	if err != nil {
		t.Fatalf("GenerateAPIKey: %v", err)
	}
	if !ValidateAPIKeyFormat(raw) {
		t.Errorf("raw key %q fails its own format check", raw)
	}
	if key.KeyPrefix != raw[:8] || key.KeyHash != hashKey(raw) || strings.Contains(key.KeyHash, raw) {
		t.Errorf("stored key = %+v, want prefix and hash of the raw key only", key)
	}

	keys, err := svc.ListUserAPIKeys(ctx, "u")
	if err != nil || len(keys) != 1 {
		t.Fatalf("ListUserAPIKeys = %v, %v", keys, err)
	}
	if err := svc.RevokeAPIKey(ctx, key.ID); err != nil || store.apiKeys[key.ID].RevokedAt == nil {
		t.Errorf("RevokeAPIKey = %v, revoked_at=%v", err, store.apiKeys[key.ID].RevokedAt)
	}

	store.fail["CreateAPIKey"] = errDB
	store.fail["ListUserAPIKeys"] = errDB
	store.fail["RevokeAPIKey"] = errDB
	if _, _, err := svc.GenerateAPIKey(ctx, "u", "x", nil); !errors.Is(err, errDB) {
		t.Errorf("GenerateAPIKey = %v, want store error", err)
	}
	if _, err := svc.ListUserAPIKeys(ctx, "u"); !errors.Is(err, errDB) {
		t.Errorf("ListUserAPIKeys = %v, want store error", err)
	}
	if err := svc.RevokeAPIKey(ctx, key.ID); !errors.Is(err, errDB) {
		t.Errorf("RevokeAPIKey = %v, want store error", err)
	}
}

func TestValidateAPIKeyFormat(t *testing.T) {
	cases := map[string]bool{
		"":                            false,
		"not base64 !!!":              false,
		"c2hvcnQ=":                    false, // valid base64, wrong length
		strings.Repeat("A", 43) + "=": true,  // 32 zero bytes
	}
	for in, want := range cases {
		if got := ValidateAPIKeyFormat(in); got != want {
			t.Errorf("ValidateAPIKeyFormat(%q) = %v, want %v", in, got, want)
		}
	}
}

func TestHTTPSessionLifecycle(t *testing.T) {
	ctx := context.Background()
	svc, store, _ := newService(t)

	sess, token, err := svc.CreateSession(ctx, "u", "10.0.0.1", "curl", time.Hour)
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	if sess.Token != hashKey(token) {
		t.Error("session stores the raw token instead of its hash")
	}
	if d := time.Until(sess.ExpiresAt); d <= 59*time.Minute || d > time.Hour {
		t.Errorf("session expires in %v, want about an hour", d)
	}
	if err := svc.DestroySession(ctx, sess.ID); err != nil || len(store.sessions) != 0 {
		t.Errorf("DestroySession = %v, sessions left %d", err, len(store.sessions))
	}
	if err := svc.CleanupExpiredSessions(ctx); err != nil || store.purgedCount != 1 {
		t.Errorf("CleanupExpiredSessions = %v, purge calls %d", err, store.purgedCount)
	}

	for _, m := range []string{"CreateHTTPSession", "DeleteHTTPSession", "DeleteExpiredHTTPSessions"} {
		store.fail[m] = errDB
	}
	if _, _, err := svc.CreateSession(ctx, "u", "", "", time.Hour); !errors.Is(err, errDB) {
		t.Errorf("CreateSession = %v, want store error", err)
	}
	if err := svc.DestroySession(ctx, "x"); !errors.Is(err, errDB) {
		t.Errorf("DestroySession = %v, want store error", err)
	}
	if err := svc.CleanupExpiredSessions(ctx); !errors.Is(err, errDB) {
		t.Errorf("CleanupExpiredSessions = %v, want store error", err)
	}
}

// --- key generation --------------------------------------------------------------------

func TestGenerateSSHKeyPairAlgo(t *testing.T) {
	cases := []struct {
		algo, wantType, wantPEM string
	}{
		{"", ssh.KeyAlgoED25519, "PRIVATE KEY"},
		{" ED25519 ", ssh.KeyAlgoED25519, "PRIVATE KEY"},
		{"rsa", ssh.KeyAlgoRSA, "RSA PRIVATE KEY"},
	}
	for _, tc := range cases {
		t.Run(tc.algo, func(t *testing.T) {
			priv, pub, err := GenerateSSHKeyPairAlgo(tc.algo)
			if err != nil {
				t.Fatalf("GenerateSSHKeyPairAlgo: %v", err)
			}
			if !strings.Contains(priv, "BEGIN "+tc.wantPEM) {
				t.Errorf("private key PEM type wrong: %.40q", priv)
			}
			signer, err := ssh.ParsePrivateKey([]byte(priv))
			if err != nil {
				t.Fatalf("private key does not parse: %v", err)
			}
			parsed, _, _, _, err := ssh.ParseAuthorizedKey([]byte(pub))
			if err != nil {
				t.Fatalf("public key does not parse: %v", err)
			}
			if parsed.Type() != tc.wantType || !keyEquals(parsed, signer.PublicKey()) {
				t.Errorf("public key type %s, matches private=%v; want %s and a matching pair", parsed.Type(), keyEquals(parsed, signer.PublicKey()), tc.wantType)
			}
		})
	}
	if _, _, err := GenerateSSHKeyPairAlgo("dsa"); err == nil {
		t.Error("GenerateSSHKeyPairAlgo(dsa) succeeded")
	}
}
