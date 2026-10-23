package api

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"

	"github.com/orion-belt-dev/orion-belt/internal/pgtest"
	"github.com/orion-belt-dev/orion-belt/pkg/auth"
	"github.com/orion-belt-dev/orion-belt/pkg/common"
	"github.com/orion-belt-dev/orion-belt/pkg/database"
	"github.com/orion-belt-dev/orion-belt/pkg/plugin"
	"github.com/orion-belt-dev/orion-belt/pkg/recording"
)

func TestMain(m *testing.M) {
	os.Exit(pgtest.Main(m))
}

// env is an API server backed by the test Postgres, with one account per role
// and a machine. Keys are raw API keys for the X-API-Key header.
type env struct {
	t        *testing.T
	srv      *APIServer
	store    database.Store
	recorder *recording.Recorder
	plugins  *plugin.Manager
	agents   *fakeCommander
	bridge   *fakeBridge
	logs     *syncBuffer

	admin, operator, auditor, alice, bob *common.User
	adminKey, operatorKey, auditorKey    string
	aliceKey, bobKey                     string
	machine                              *common.Machine
}

type syncBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *syncBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

func openStore(t *testing.T) database.Store {
	t.Helper()
	store, err := database.NewPostgresStore(pgtest.DSN(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	ctx := context.Background()
	if err := store.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	return store
}

// resetTables empties the database through a raw connection.
func resetTables(t *testing.T) {
	t.Helper()
	db, err := sqlOpen(pgtest.DSN(t))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(`TRUNCATE users, machines, permission_history, plugin_settings, notification_policy,
		notification_templates, ssh_ca_keys, ssh_certificates CASCADE`); err != nil {
		t.Fatal(err)
	}
}

func newEnv(t *testing.T, mutate func(*Options)) *env {
	t.Helper()
	store := openStore(t)
	resetTables(t)

	logs := &syncBuffer{}
	logger := common.NewLoggerTo(common.DEBUG, logs)
	rec, err := recording.NewRecorder(t.TempDir(), logger)
	if err != nil {
		t.Fatal(err)
	}
	pm := plugin.NewManager(logger)
	e := &env{t: t, store: store, recorder: rec, plugins: pm, agents: &fakeCommander{}, bridge: &fakeBridge{}, logs: logs}

	opt := Options{
		JWTSecret:      "test-jwt-secret",
		PluginManager:  pm,
		AgentCommander: e.agents,
		TerminalBridge: e.bridge,
		Recorder:       rec,
		MetricsEnabled: true,
		Server:         common.ServerConfig{PublicURL: "https://gw.example.com", Port: 2222, APIPort: 8080},
	}
	if mutate != nil {
		mutate(&opt)
	}
	e.srv = NewAPIServer(store, auth.NewAuthService(store, logger), logger, opt)

	e.admin, e.adminKey = e.user("admin", common.RoleAdmin)
	e.operator, e.operatorKey = e.user("operator", common.RoleOperator)
	e.auditor, e.auditorKey = e.user("auditor", common.RoleAuditor)
	e.alice, e.aliceKey = e.user("alice", common.RoleUser)
	e.bob, e.bobKey = e.user("bob", common.RoleUser)

	e.machine = common.NewMachine("web-01", "10.0.0.1", 22, map[string]string{"env": "prod"})
	if err := store.CreateMachine(context.Background(), e.machine); err != nil {
		t.Fatal(err)
	}
	return e
}

func (e *env) user(name, role string) (*common.User, string) {
	e.t.Helper()
	u := common.NewUser(name, name+"@example.com", testPubKey, role == common.RoleAdmin)
	u.Role = role
	if err := e.store.CreateUser(context.Background(), u); err != nil {
		e.t.Fatal(err)
	}
	_, raw, err := e.srv.authService.GenerateAPIKey(context.Background(), u.ID, "test", nil)
	if err != nil {
		e.t.Fatal(err)
	}
	return u, raw
}

// resp is a decoded response.
type resp struct {
	code int
	body []byte
	hdr  http.Header
}

func (r resp) json(t *testing.T, v any) {
	t.Helper()
	if err := json.Unmarshal(r.body, v); err != nil {
		t.Fatalf("decode %q: %v", r.body, err)
	}
}

func (r resp) obj(t *testing.T) map[string]any {
	t.Helper()
	var m map[string]any
	r.json(t, &m)
	return m
}

func (r resp) list(t *testing.T) []map[string]any {
	t.Helper()
	var l []map[string]any
	r.json(t, &l)
	return l
}

// do sends a request with an API key (empty for anonymous). body may be a
// string (sent raw) or any JSON-encodable value.
func (e *env) do(method, path, key string, body any, hdr ...string) resp {
	e.t.Helper()
	var r io.Reader
	switch b := body.(type) {
	case nil:
	case string:
		r = strings.NewReader(b)
	default:
		raw, err := json.Marshal(b)
		if err != nil {
			e.t.Fatal(err)
		}
		r = bytes.NewReader(raw)
	}
	req := httptest.NewRequest(method, path, r)
	req.RemoteAddr = "192.0.2.10:40000"
	req.Header.Set("Content-Type", "application/json")
	if key != "" {
		req.Header.Set("X-API-Key", key)
	}
	for i := 0; i+1 < len(hdr); i += 2 {
		req.Header.Set(hdr[i], hdr[i+1])
	}
	w := httptest.NewRecorder()
	e.srv.Router().ServeHTTP(w, req)
	return resp{code: w.Code, body: w.Body.Bytes(), hdr: w.Header()}
}

func (e *env) expect(r resp, code int) {
	e.t.Helper()
	if r.code != code {
		e.t.Fatalf("status = %d, want %d: %s", r.code, code, r.body)
	}
}

// auditActions returns the actions recorded so far, newest first.
func (e *env) auditActions() []string {
	e.t.Helper()
	logs, err := e.store.ListAuditLogs(context.Background(), 200, 0, nil)
	if err != nil {
		e.t.Fatal(err)
	}
	out := make([]string, len(logs))
	for i, l := range logs {
		out[i] = l.Action
	}
	return out
}

func (e *env) audited(action string) bool {
	for _, a := range e.auditActions() {
		if a == action {
			return true
		}
	}
	return false
}

// --- fakes at process boundaries ---------------------------------------------------

type fakeCommander struct {
	mu           sync.Mutex
	connected    []string
	output       []byte
	err          error
	disconnected []string
}

func (f *fakeCommander) SendAgentCommand(machineID, command string) ([]byte, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.output, f.err
}

func (f *fakeCommander) ListConnectedAgents() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.connected
}

func (f *fakeCommander) DisconnectAgent(machineID string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.disconnected = append(f.disconnected, machineID)
	return f.err
}

// fakeBridge stands in for the gateway's agent connections: each session it
// opens is one end of an in-memory SSH channel pair served by handle.
type fakeBridge struct {
	mu       sync.Mutex
	handle   func(cmd string, stdin []byte) (stdout string, status uint32)
	machines map[string]*common.Machine
	openErr  error
}

func (b *fakeBridge) ResolveMachine(name string) (*common.Machine, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if m, ok := b.machines[name]; ok {
		return m, nil
	}
	return nil, database.ErrNotFound
}

func (b *fakeBridge) OpenAgentSession(machineID, remoteUser string) (ssh.Channel, <-chan *ssh.Request, error) {
	b.mu.Lock()
	err, handle := b.openErr, b.handle
	b.mu.Unlock()
	if err != nil {
		return nil, nil, err
	}
	return pipeSession(handle)
}

// --- broken store --------------------------------------------------------------------

// brokenStore fails every data call while still authenticating callers, so a
// server built on it shows how each handler reports a storage failure.
type brokenStore struct {
	database.Store // a closed store: every call errors
	live           database.Store
}

func newBrokenStore(t *testing.T, live database.Store) *brokenStore {
	t.Helper()
	closed, err := database.NewPostgresStore(pgtest.DSN(t))
	if err != nil {
		t.Fatal(err)
	}
	_ = closed.Close()
	return &brokenStore{Store: closed, live: live}
}

func (b *brokenStore) GetAPIKeyByHash(ctx context.Context, h string) (*common.APIKey, error) {
	return b.live.GetAPIKeyByHash(ctx, h)
}

func (b *brokenStore) GetUser(ctx context.Context, id string) (*common.User, error) {
	return b.live.GetUser(ctx, id)
}

func (b *brokenStore) UpdateAPIKeyLastUsed(ctx context.Context, id string, at time.Time) error {
	return b.live.UpdateAPIKeyLastUsed(ctx, id, at)
}

// brokenServerWith builds a server over a failing store sharing this env's
// fakes, accounts and log buffer.
func (e *env) brokenServerWith(mutate func(*Options)) *APIServer {
	e.t.Helper()
	bs := newBrokenStore(e.t, e.store)
	logger := common.NewLoggerTo(common.DEBUG, e.logs)
	opt := Options{
		JWTSecret: "test-jwt-secret", PluginManager: e.plugins, AgentCommander: e.agents,
		TerminalBridge: e.bridge, Recorder: e.recorder,
	}
	if mutate != nil {
		mutate(&opt)
	}
	return NewAPIServer(bs, auth.NewAuthService(bs, logger), logger, opt)
}

func writeFile(t *testing.T, dir, name, content string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}
