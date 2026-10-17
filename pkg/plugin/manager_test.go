package plugin

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
)

// basicPlugin is a plain Plugin: no hooks, no schema, no HTTP handler.
type basicPlugin struct {
	name        string
	initErr     error
	shutdownErr error
	initCalls   int
	gotConfig   map[string]interface{}
	shutdowns   int
}

func (b *basicPlugin) Name() string    { return b.name }
func (b *basicPlugin) Version() string { return "1.0.0" }
func (b *basicPlugin) Initialize(_ context.Context, cfg map[string]interface{}) error {
	b.initCalls++
	b.gotConfig = cfg
	return b.initErr
}
func (b *basicPlugin) Shutdown(context.Context) error { b.shutdowns++; return b.shutdownErr }

// richPlugin implements every optional interface.
type richPlugin struct {
	basicPlugin
	hooks []Hook
}

func (r *richPlugin) OnHook(_ context.Context, hook Hook, _ *HookContext) error {
	r.hooks = append(r.hooks, hook)
	return nil
}

func (r *richPlugin) ConfigSchema() []ConfigField {
	return []ConfigField{
		{Key: "channel", Type: "string"},
		{Key: "slack", Type: "object", Fields: []ConfigField{
			{Key: "bot_credential", Type: "string", Secret: true},
		}},
	}
}

func (r *richPlugin) Handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusTeapot)
	})
}

func TestManager_RegisterGetListUnregister(t *testing.T) {
	m := newTestManager(t)
	a := &basicPlugin{name: "a"}
	b := &richPlugin{basicPlugin: basicPlugin{name: "b"}}

	for _, p := range []Plugin{a, b} {
		if err := m.Register(p); err != nil {
			t.Fatalf("Register(%s): %v", p.Name(), err)
		}
	}
	if err := m.Register(&basicPlugin{name: "a"}); err == nil {
		t.Error("registering a duplicate name succeeded")
	}
	if got, err := m.Get("b"); err != nil || got != b {
		t.Errorf("Get(b) = %v, %v; want the registered plugin", got, err)
	}
	if _, err := m.Get("missing"); err == nil {
		t.Error("Get(missing) succeeded")
	}
	if n := len(m.List()); n != 2 {
		t.Errorf("List() has %d plugins, want 2", n)
	}
	if n := len(m.hookPlugins[HookSessionStart]); n != 1 {
		t.Fatalf("hook plugins for session_start = %d, want only the HookPlugin", n)
	}

	if err := m.Unregister("b"); err != nil {
		t.Fatalf("Unregister(b): %v", err)
	}
	if err := m.Unregister("a"); err != nil {
		t.Fatalf("Unregister(a): %v", err)
	}
	if err := m.Unregister("a"); err == nil {
		t.Error("Unregister of an already removed plugin succeeded")
	}
	for hook, ps := range m.hookPlugins {
		if len(ps) != 0 {
			t.Errorf("hook %s still has %d plugins after Unregister", hook, len(ps))
		}
	}
	if err := m.TriggerHook(context.Background(), HookSessionStart, &HookContext{}); err != nil {
		t.Errorf("TriggerHook after unregister: %v", err)
	}
	if len(b.hooks) != 0 {
		t.Errorf("unregistered plugin still received hooks: %v", b.hooks)
	}
}

func TestManager_DisabledPluginGetsNoHooksAndNoHTTP(t *testing.T) {
	m := newTestManager(t)
	p := &richPlugin{basicPlugin: basicPlugin{name: "chat"}}
	if err := m.Register(p); err != nil {
		t.Fatal(err)
	}

	h, ok := m.HTTPHandler("chat")
	if !ok {
		t.Fatal("HTTPHandler for an enabled HTTPPlugin returned false")
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/", nil))
	if rec.Code != http.StatusTeapot {
		t.Errorf("handler status = %d, want the plugin's own handler", rec.Code)
	}

	if err := m.SetEnabled("chat", false); err != nil {
		t.Fatalf("SetEnabled: %v", err)
	}
	if err := m.TriggerHook(context.Background(), HookPostAuth, &HookContext{}); err != nil {
		t.Fatalf("TriggerHook: %v", err)
	}
	if len(p.hooks) != 0 {
		t.Errorf("disabled plugin received hooks %v", p.hooks)
	}
	if _, ok := m.HTTPHandler("chat"); ok {
		t.Error("HTTPHandler served a disabled plugin")
	}

	if err := m.SetEnabled("missing", true); err == nil {
		t.Error("SetEnabled on an unknown plugin succeeded")
	}
}

func TestManager_HTTPHandler_NotAvailable(t *testing.T) {
	m := newTestManager(t)
	if err := m.Register(&basicPlugin{name: "plain"}); err != nil {
		t.Fatal(err)
	}
	if _, ok := m.HTTPHandler("plain"); ok {
		t.Error("HTTPHandler returned a handler for a plugin without one")
	}
	if _, ok := m.HTTPHandler("missing"); ok {
		t.Error("HTTPHandler returned a handler for an unknown plugin")
	}

	// A state without a plugin should not happen, but must not panic.
	m.states["orphan"] = &pluginState{enabled: true}
	if _, ok := m.HTTPHandler("orphan"); ok {
		t.Error("HTTPHandler returned a handler for an orphaned state")
	}
}

func TestManager_SetEnabledAndConfigureRecreateMissingState(t *testing.T) {
	m := newTestManager(t)
	p := &basicPlugin{name: "p"}
	if err := m.Register(p); err != nil {
		t.Fatal(err)
	}

	delete(m.states, "p")
	if err := m.SetEnabled("p", true); err != nil || !m.states["p"].enabled {
		t.Fatalf("SetEnabled did not recreate state: err=%v state=%+v", err, m.states["p"])
	}
	delete(m.states, "p")
	if err := m.Configure(context.Background(), "p", map[string]interface{}{}); err != nil {
		t.Fatalf("Configure: %v", err)
	}
	if st := m.states["p"]; st == nil || !st.enabled || !st.configured {
		t.Errorf("Configure did not recreate an enabled, configured state: %+v", st)
	}
}

func TestManager_ConfigureRecordsOutcome(t *testing.T) {
	m := newTestManager(t)
	p := &basicPlugin{name: "p", initErr: errors.New("missing url")}
	if err := m.Register(p); err != nil {
		t.Fatal(err)
	}

	err := m.Configure(context.Background(), "p", map[string]interface{}{"url": ""})
	if !errors.Is(err, p.initErr) {
		t.Fatalf("Configure error = %v, want it to wrap the init error", err)
	}
	info, _ := m.Info("p")
	if info.Configured || info.LastError != "missing url" {
		t.Errorf("Info after failure = %+v, want unconfigured with last error", info)
	}

	p.initErr = nil
	if err := m.Configure(context.Background(), "p", map[string]interface{}{"url": "x"}); err != nil {
		t.Fatalf("Configure: %v", err)
	}
	info, _ = m.Info("p")
	if !info.Configured || info.LastError != "" || info.Config["url"] != "x" {
		t.Errorf("Info after success = %+v, want configured, no error, config kept", info)
	}

	if err := m.Configure(context.Background(), "missing", nil); err == nil {
		t.Error("Configure on an unknown plugin succeeded")
	}
}

func TestManager_InitializeAll(t *testing.T) {
	m := newTestManager(t)
	good := &basicPlugin{name: "good"}
	bad := &basicPlugin{name: "bad", initErr: errors.New("nope")}
	for _, p := range []Plugin{good, bad} {
		if err := m.Register(p); err != nil {
			t.Fatal(err)
		}
	}

	failed := m.InitializeAll(context.Background(), map[string]map[string]interface{}{
		"good": {"k": "v"},
	})
	if len(failed) != 1 || failed["bad"] == nil {
		t.Errorf("InitializeAll failures = %v, want only bad", failed)
	}
	if good.gotConfig["k"] != "v" {
		t.Errorf("good plugin config = %v, want its section", good.gotConfig)
	}
	if bad.gotConfig == nil {
		t.Error("plugin without a config section got nil instead of an empty map")
	}

	bad.initErr = nil
	if failed := m.InitializeAll(context.Background(), nil); failed != nil {
		t.Errorf("InitializeAll with all succeeding = %v, want nil", failed)
	}
}

func TestManager_InfoAndListInfo(t *testing.T) {
	m := newTestManager(t)
	rich := &richPlugin{basicPlugin: basicPlugin{name: "zeta"}}
	plain := &basicPlugin{name: "alpha"}
	for _, p := range []Plugin{rich, plain} {
		if err := m.Register(p); err != nil {
			t.Fatal(err)
		}
	}
	cfg := map[string]interface{}{
		"channel": "#ops",
		"slack":   map[string]interface{}{"bot_credential": "xoxb-1234567890abcdef"},
	}
	if err := m.Configure(context.Background(), "zeta", cfg); err != nil {
		t.Fatal(err)
	}

	list := m.ListInfo()
	if len(list) != 2 || list[0].Name != "alpha" || list[1].Name != "zeta" {
		t.Fatalf("ListInfo order = %v, want alpha, zeta", []string{list[0].Name, list[1].Name})
	}
	if list[0].HasWebhook || list[0].Schema != nil || len(list[0].Config) != 0 {
		t.Errorf("plain plugin info = %+v", list[0])
	}

	z := list[1]
	if !z.HasWebhook || len(z.Schema) != 2 || !z.Enabled || !z.Configured {
		t.Errorf("rich plugin info = %+v", z)
	}
	slack := z.Config["slack"].(map[string]interface{})
	if got := slack["bot_credential"]; got != "xoxb****cdef" {
		t.Errorf("schema-declared secret = %v, want it masked", got)
	}
	if z.Config["channel"] != "#ops" {
		t.Errorf("non-secret value = %v, want it unchanged", z.Config["channel"])
	}

	if _, err := m.Info("missing"); err == nil {
		t.Error("Info(missing) succeeded")
	}
}

func TestManager_SchemaSecrets(t *testing.T) {
	m := newTestManager(t)
	_ = m.Register(&richPlugin{basicPlugin: basicPlugin{name: "rich"}})
	_ = m.Register(&basicPlugin{name: "plain"})

	got, err := m.SchemaSecrets("rich")
	if err != nil || !reflect.DeepEqual(got, map[string]bool{"bot_credential": true}) {
		t.Errorf("SchemaSecrets(rich) = %v, %v", got, err)
	}
	if got, err := m.SchemaSecrets("plain"); err != nil || got != nil {
		t.Errorf("SchemaSecrets(plain) = %v, %v; want nil, nil", got, err)
	}
	if _, err := m.SchemaSecrets("missing"); err == nil {
		t.Error("SchemaSecrets(missing) succeeded")
	}
}

func TestManager_ShutdownAllContinuesPastErrors(t *testing.T) {
	m := newTestManager(t)
	a := &basicPlugin{name: "a", shutdownErr: errors.New("stuck")}
	b := &basicPlugin{name: "b"}
	_ = m.Register(a)
	_ = m.Register(b)

	if err := m.ShutdownAll(context.Background()); err != nil {
		t.Fatalf("ShutdownAll = %v, want nil (errors are logged)", err)
	}
	if a.shutdowns != 1 || b.shutdowns != 1 {
		t.Errorf("shutdown calls a=%d b=%d, want each called once", a.shutdowns, b.shutdowns)
	}
}

func TestCallHook_ContextCancelled(t *testing.T) {
	m := newTestManager(t)
	block := make(chan struct{})
	t.Cleanup(func() { close(block) })
	p := &fakeHookPlugin{name: "slow", fn: func(context.Context, Hook, *HookContext) error {
		<-block
		return nil
	}}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := m.callHook(ctx, p, HookPreAuth, &HookContext{}); !errors.Is(err, context.Canceled) {
		t.Errorf("callHook error = %v, want context.Canceled", err)
	}
}

func TestRedactConfig(t *testing.T) {
	in := map[string]interface{}{
		"password":    "short",
		"api_key":     42,
		"webhook_url": "https://hooks.example.com/services/T000/B000/XXXX",
		"targets": []interface{}{
			map[string]interface{}{"token": "abcdefghij"},
			"plain",
		},
		"retries": 3,
	}
	got := redactConfig(in, nil)
	want := map[string]interface{}{
		"password":    "********",
		"api_key":     "********",
		"webhook_url": "http****XXXX",
		"targets": []interface{}{
			map[string]interface{}{"token": "ab****ij"},
			"plain",
		},
		"retries": 3,
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("redactConfig =\n%v\nwant\n%v", got, want)
	}
	if in["password"] != "short" {
		t.Error("redactConfig mutated its input")
	}
}

func TestSecretLeafNames_EmptySchema(t *testing.T) {
	if got := secretLeafNames(nil); got != nil {
		t.Errorf("secretLeafNames(nil) = %v, want nil", got)
	}
}

func TestReconcileConfig_NonStringAndNewValues(t *testing.T) {
	old := map[string]interface{}{"token": "abcdefghijklmnop", "sub": "not a map"}
	in := map[string]interface{}{
		"retries": 5,
		"token":   "brand-new-token-value",
		"sub":     map[string]interface{}{"password": "********"},
	}
	got := ReconcileConfig(in, old, nil)
	if got["retries"] != 5 || got["token"] != "brand-new-token-value" {
		t.Errorf("ReconcileConfig = %v, want non-strings and rotated secrets kept", got)
	}
	if sub := got["sub"].(map[string]interface{}); sub["password"] != "********" {
		t.Errorf("nested value with no old map = %v, want it kept as submitted", sub)
	}
}
