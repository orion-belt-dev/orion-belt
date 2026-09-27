package api

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/orion-belt-dev/orion-belt/pkg/auth"
	"github.com/orion-belt-dev/orion-belt/pkg/common"
	"github.com/orion-belt-dev/orion-belt/pkg/database"
)

// secStore is an in-memory stand-in for the user/machine/API-key/permission
// slice of the store that the auth and registration paths touch.
type secStore struct {
	database.Store

	mu       sync.Mutex
	users    map[string]*common.User
	machines map[string]*common.Machine
	apiKeys  map[string]*common.APIKey // by hash
	perms    []*common.Permission
}

func newSecStore() *secStore {
	return &secStore{
		users:    map[string]*common.User{},
		machines: map[string]*common.Machine{},
		apiKeys:  map[string]*common.APIKey{},
	}
}

func (s *secStore) CreateUser(_ context.Context, u *common.User) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.users[u.ID] = u
	return nil
}

func (s *secStore) GetUser(_ context.Context, id string) (*common.User, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if u, ok := s.users[id]; ok {
		cp := *u
		return &cp, nil
	}
	return nil, database.ErrNotFound
}

func (s *secStore) GetUserByUsername(_ context.Context, name string) (*common.User, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, u := range s.users {
		if u.Username == name {
			cp := *u
			return &cp, nil
		}
	}
	return nil, database.ErrNotFound
}

func (s *secStore) UpdateUser(_ context.Context, u *common.User) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.users[u.ID] = u
	return nil
}

func (s *secStore) DeleteUser(_ context.Context, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.users, id)
	return nil
}

func (s *secStore) ListUsers(_ context.Context, limit, _ int) ([]*common.User, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []*common.User
	for _, u := range s.users {
		if len(out) >= limit {
			break
		}
		out = append(out, u)
	}
	return out, nil
}

func (s *secStore) GetMachineByName(_ context.Context, name string) (*common.Machine, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, m := range s.machines {
		if m.Name == name {
			return m, nil
		}
	}
	return nil, database.ErrNotFound
}

func (s *secStore) CreateMachine(_ context.Context, m *common.Machine) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.machines[m.ID] = m
	return nil
}

func (s *secStore) GetAPIKeyByHash(_ context.Context, hash string) (*common.APIKey, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if k, ok := s.apiKeys[hash]; ok {
		return k, nil
	}
	return nil, database.ErrNotFound
}

func (s *secStore) UpdateAPIKeyLastUsed(context.Context, string, time.Time) error { return nil }
func (s *secStore) CreateAuditLog(context.Context, *common.AuditLog) error         { return nil }

func (s *secStore) ListUserPermissions(_ context.Context, userID string) ([]*common.Permission, error) {
	var out []*common.Permission
	for _, p := range s.perms {
		if p.UserID == userID {
			out = append(out, p)
		}
	}
	return out, nil
}

func (s *secStore) ListMachinePermissions(_ context.Context, machineID string) ([]*common.Permission, error) {
	var out []*common.Permission
	for _, p := range s.perms {
		if p.MachineID == machineID {
			out = append(out, p)
		}
	}
	return out, nil
}

// addUser creates a user with the given role and returns it with a raw API
// key that authenticates as them.
func (s *secStore) addUser(t *testing.T, name, role string) (*common.User, string) {
	t.Helper()
	u := common.NewUser(name, name+"@example.com", "", role == common.RoleAdmin)
	u.Role = role
	_ = s.CreateUser(context.Background(), u)
	raw := "key-" + u.ID
	s.apiKeys[hashAPIKey(raw)] = &common.APIKey{ID: "k-" + u.ID, UserID: u.ID, Name: "test"}
	return u, raw
}

func newSecServer(t *testing.T, store *secStore, opt Options) *APIServer {
	t.Helper()
	logger := common.NewLoggerTo(common.FATAL, io.Discard)
	return NewAPIServer(store, auth.NewAuthService(store, logger), logger, opt)
}

func doJSON(t *testing.T, s *APIServer, method, path, apiKey string, body interface{}, hdr map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	var r io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		r = bytes.NewReader(b)
	}
	req := httptest.NewRequest(method, path, r)
	req.RemoteAddr = "192.0.2.10:40000"
	req.Header.Set("Content-Type", "application/json")
	if apiKey != "" {
		req.Header.Set("X-API-Key", apiKey)
	}
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	w := httptest.NewRecorder()
	s.Router().ServeHTTP(w, req)
	return w
}

const testPubKey = "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIOMqqnkVzrm0SdG6UOoqKLsabgH5C9okWi0dh2l9GKJl test"

func TestRegisterClientRejectsAnonymousAdminAfterBootstrap(t *testing.T) {
	store := newSecStore()
	store.addUser(t, "root-admin", common.RoleAdmin)
	s := newSecServer(t, store, Options{})

	w := doJSON(t, s, http.MethodPost, "/api/v1/public/register/client", "", map[string]interface{}{
		"username": "mallory", "email": "m@example.com", "public_key": testPubKey, "is_admin": true,
	}, nil)
	if w.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403; body=%s", w.Code, w.Body)
	}
	if u, _ := store.GetUserByUsername(context.Background(), "mallory"); u != nil {
		t.Fatal("admin self-registration must not create the account")
	}
}

func TestRegisterClientNonAdminCallerCannotCreateAdmin(t *testing.T) {
	store := newSecStore()
	store.addUser(t, "root-admin", common.RoleAdmin)
	_, opKey := store.addUser(t, "op", common.RoleOperator)
	s := newSecServer(t, store, Options{})

	w := doJSON(t, s, http.MethodPost, "/api/v1/public/register/client", opKey, map[string]interface{}{
		"username": "sneaky", "email": "s@example.com", "public_key": testPubKey, "is_admin": true,
	}, nil)
	if w.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403; body=%s", w.Code, w.Body)
	}
}

func TestRegisterClientFirstRunBootstrapCreatesAdmin(t *testing.T) {
	store := newSecStore()
	s := newSecServer(t, store, Options{})

	w := doJSON(t, s, http.MethodPost, "/api/v1/public/register/client", "", map[string]interface{}{
		"username": "admin", "email": "a@example.com", "public_key": testPubKey, "is_admin": true,
	}, nil)
	if w.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201; body=%s", w.Code, w.Body)
	}
	u, err := store.GetUserByUsername(context.Background(), "admin")
	if err != nil || !u.IsAdmin {
		t.Fatalf("first-run admin not created: %+v %v", u, err)
	}
}

func TestRegisterClientAdminCallerCanCreateAdmin(t *testing.T) {
	store := newSecStore()
	_, adminKey := store.addUser(t, "root-admin", common.RoleAdmin)
	s := newSecServer(t, store, Options{})

	w := doJSON(t, s, http.MethodPost, "/api/v1/public/register/client", adminKey, map[string]interface{}{
		"username": "second-admin", "email": "b@example.com", "public_key": testPubKey, "is_admin": true,
	}, nil)
	if w.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201; body=%s", w.Code, w.Body)
	}
}

func TestRegisterClientAnonymousPlainUserStillAllowed(t *testing.T) {
	store := newSecStore()
	store.addUser(t, "root-admin", common.RoleAdmin)
	s := newSecServer(t, store, Options{})

	w := doJSON(t, s, http.MethodPost, "/api/v1/public/register/client", "", map[string]interface{}{
		"username": "carol", "email": "c@example.com", "public_key": testPubKey,
	}, nil)
	if w.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201; body=%s", w.Code, w.Body)
	}
	u, _ := store.GetUserByUsername(context.Background(), "carol")
	if u == nil || u.IsAdmin || u.EffectiveRole() != common.RoleUser {
		t.Fatalf("self-registered user should be a plain user, got %+v", u)
	}
}

func TestRegisterAgentRequiresOperator(t *testing.T) {
	store := newSecStore()
	_, userKey := store.addUser(t, "alice", common.RoleUser)
	_, opKey := store.addUser(t, "op", common.RoleOperator)
	s := newSecServer(t, store, Options{})

	body := func(name string) map[string]interface{} {
		return map[string]interface{}{"name": name, "hostname": name, "port": 22, "public_key": testPubKey}
	}

	if w := doJSON(t, s, http.MethodPost, "/api/v1/public/register/agent", "", body("anon-01"), nil); w.Code != http.StatusUnauthorized {
		t.Fatalf("anonymous: status = %d, want 401; body=%s", w.Code, w.Body)
	}
	if w := doJSON(t, s, http.MethodPost, "/api/v1/public/register/agent", userKey, body("user-01"), nil); w.Code != http.StatusForbidden {
		t.Fatalf("plain user: status = %d, want 403; body=%s", w.Code, w.Body)
	}
	if _, err := store.GetMachineByName(context.Background(), "anon-01"); err == nil {
		t.Fatal("anonymous agent registration must not create a machine")
	}
	if w := doJSON(t, s, http.MethodPost, "/api/v1/public/register/agent", opKey, body("web-01"), nil); w.Code != http.StatusCreated {
		t.Fatalf("operator: status = %d, want 201; body=%s", w.Code, w.Body)
	}
}

func TestOperatorCannotEscalateToAdmin(t *testing.T) {
	store := newSecStore()
	admin, _ := store.addUser(t, "root-admin", common.RoleAdmin)
	op, opKey := store.addUser(t, "op", common.RoleOperator)
	alice, _ := store.addUser(t, "alice", common.RoleUser)
	s := newSecServer(t, store, Options{})

	cases := []struct {
		name   string
		method string
		path   string
		body   interface{}
	}{
		{"promote self via role", http.MethodPut, "/api/v1/admin/users/" + op.ID, map[string]string{"role": "admin"}},
		{"promote self via is_admin", http.MethodPut, "/api/v1/admin/users/" + op.ID, map[string]bool{"is_admin": true}},
		{"promote another user", http.MethodPut, "/api/v1/admin/users/" + alice.ID, map[string]string{"role": "admin"}},
		{"take over admin key", http.MethodPut, "/api/v1/admin/users/" + admin.ID, map[string]string{"public_key": testPubKey}},
		{"create admin", http.MethodPost, "/api/v1/admin/users", map[string]string{"username": "x", "email": "x@example.com", "role": "admin"}},
		{"delete admin", http.MethodDelete, "/api/v1/admin/users/" + admin.ID, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			w := doJSON(t, s, tc.method, tc.path, opKey, tc.body, nil)
			if w.Code != http.StatusForbidden {
				t.Fatalf("status = %d, want 403; body=%s", w.Code, w.Body)
			}
		})
	}

	if u, _ := store.GetUser(context.Background(), op.ID); u.HasRole(common.RoleAdmin) {
		t.Fatal("operator was promoted to admin")
	}
	if u, _ := store.GetUser(context.Background(), admin.ID); u == nil || u.PublicKey != "" {
		t.Fatal("admin account was modified or deleted by an operator")
	}
}

func TestOperatorCanStillManagePlainUsers(t *testing.T) {
	store := newSecStore()
	_, opKey := store.addUser(t, "op", common.RoleOperator)
	alice, _ := store.addUser(t, "alice", common.RoleUser)
	s := newSecServer(t, store, Options{})

	w := doJSON(t, s, http.MethodPut, "/api/v1/admin/users/"+alice.ID, opKey, map[string]string{"role": "auditor"}, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", w.Code, w.Body)
	}
}

func TestAdminCanPromoteUser(t *testing.T) {
	store := newSecStore()
	_, adminKey := store.addUser(t, "root-admin", common.RoleAdmin)
	alice, _ := store.addUser(t, "alice", common.RoleUser)
	s := newSecServer(t, store, Options{})

	w := doJSON(t, s, http.MethodPut, "/api/v1/admin/users/"+alice.ID, adminKey, map[string]string{"role": "admin"}, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", w.Code, w.Body)
	}
}

func TestApproveRejectsSpoofedReviewer(t *testing.T) {
	store := newSecStore()
	_, adminKey := store.addUser(t, "root-admin", common.RoleAdmin)
	other, _ := store.addUser(t, "other-admin", common.RoleAdmin)
	s := newSecServer(t, store, Options{})

	for _, action := range []string{"approve", "reject"} {
		w := doJSON(t, s, http.MethodPost, "/api/v1/admin/access-requests/req-1/"+action, adminKey,
			map[string]string{"reviewer_id": other.ID}, nil)
		if w.Code != http.StatusForbidden {
			t.Fatalf("%s: status = %d, want 403; body=%s", action, w.Code, w.Body)
		}
	}
}

func TestGetUserPermissionsScopedToCaller(t *testing.T) {
	store := newSecStore()
	alice, aliceKey := store.addUser(t, "alice", common.RoleUser)
	bob, _ := store.addUser(t, "bob", common.RoleUser)
	_, auditorKey := store.addUser(t, "aud", common.RoleAuditor)
	store.perms = []*common.Permission{
		{ID: "p1", UserID: alice.ID, MachineID: "m1"},
		{ID: "p2", UserID: bob.ID, MachineID: "m1"},
	}
	s := newSecServer(t, store, Options{})

	if w := doJSON(t, s, http.MethodGet, "/api/v1/permissions/user/"+bob.ID, aliceKey, nil, nil); w.Code != http.StatusForbidden {
		t.Fatalf("other user's perms: status = %d, want 403", w.Code)
	}
	if w := doJSON(t, s, http.MethodGet, "/api/v1/permissions/user/"+alice.ID, aliceKey, nil, nil); w.Code != http.StatusOK {
		t.Fatalf("own perms: status = %d, want 200", w.Code)
	}
	if w := doJSON(t, s, http.MethodGet, "/api/v1/permissions/user/"+bob.ID, auditorKey, nil, nil); w.Code != http.StatusOK {
		t.Fatalf("auditor: status = %d, want 200", w.Code)
	}

	w := doJSON(t, s, http.MethodGet, "/api/v1/permissions/machine/m1", aliceKey, nil, nil)
	var got []common.Permission
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v (%s)", err, w.Body)
	}
	if len(got) != 1 || got[0].UserID != alice.ID {
		t.Fatalf("plain user should only see own grants on the machine, got %+v", got)
	}
}

func TestPublicAuthEndpointsRateLimited(t *testing.T) {
	s := newSecServer(t, newSecStore(), Options{})

	var last int
	for i := 0; i < publicAuthRateLimitPerMinute+1; i++ {
		last = doJSON(t, s, http.MethodPost, "/api/v1/public/auth/challenge", "", map[string]string{"username": "x"}, nil).Code
	}
	if last != http.StatusTooManyRequests {
		t.Fatalf("request %d: status = %d, want 429", publicAuthRateLimitPerMinute+1, last)
	}
}

func TestPublicRateLimitIgnoresSpoofedForwardedFor(t *testing.T) {
	s := newSecServer(t, newSecStore(), Options{})

	var last int
	for i := 0; i < publicAuthRateLimitPerMinute+1; i++ {
		// A fresh X-Forwarded-For per request must not buy a fresh bucket.
		hdr := map[string]string{"X-Forwarded-For": "198.51.100." + itoa(i)}
		last = doJSON(t, s, http.MethodPost, "/api/v1/public/auth/challenge", "", map[string]string{"username": "x"}, hdr).Code
	}
	if last != http.StatusTooManyRequests {
		t.Fatalf("spoofed X-Forwarded-For bypassed the limiter (last status %d)", last)
	}
}

func TestTrustedProxyForwardedForHonored(t *testing.T) {
	s := newSecServer(t, newSecStore(), Options{Server: common.ServerConfig{TrustedProxies: []string{"192.0.2.0/24"}}})

	for i := 0; i < publicAuthRateLimitPerMinute+1; i++ {
		hdr := map[string]string{"X-Forwarded-For": "198.51.100." + itoa(i)}
		if code := doJSON(t, s, http.MethodPost, "/api/v1/public/auth/challenge", "", map[string]string{"username": "x"}, hdr).Code; code != http.StatusOK {
			t.Fatalf("request %d via trusted proxy: status = %d, want 200 (distinct clients)", i, code)
		}
	}
}

func itoa(i int) string {
	b, _ := json.Marshal(i)
	return string(b)
}

func TestCheckWSOrigin(t *testing.T) {
	cases := []struct {
		name   string
		host   string
		origin string
		want   bool
	}{
		{"no origin (CLI client)", "gw.example.com", "", true},
		{"same origin", "gw.example.com", "https://gw.example.com", true},
		{"same origin with port", "gw.example.com:8080", "http://gw.example.com:8080", true},
		{"same origin case-insensitive", "GW.example.com", "https://gw.EXAMPLE.com", true},
		{"cross-site", "gw.example.com", "https://evil.example.net", false},
		{"sibling subdomain", "gw.example.com", "https://blog.example.com", false},
		{"port mismatch", "gw.example.com:8080", "http://gw.example.com:9999", false},
		{"vite dev proxy", "127.0.0.1:8080", "http://localhost:5173", true},
		{"ipv6 loopback dev", "[::1]:8080", "http://[::1]:5173", true},
		{"loopback origin against remote host", "gw.example.com", "http://localhost:5173", false},
		{"null origin", "gw.example.com", "null", false},
		{"garbage origin", "gw.example.com", "://", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodGet, "/api/v1/terminal/ws", nil)
			r.Host = tc.host
			if tc.origin != "" {
				r.Header.Set("Origin", tc.origin)
			}
			if got := checkWSOrigin(r); got != tc.want {
				t.Fatalf("checkWSOrigin(host=%q, origin=%q) = %v, want %v", tc.host, tc.origin, got, tc.want)
			}
		})
	}
}
