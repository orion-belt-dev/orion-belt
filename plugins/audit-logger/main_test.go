package auditlogger

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/orion-belt-dev/orion-belt/pkg/plugin"
)

func TestAuditPluginWritesEveryHook(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "nested", "audit.log")
	p := NewPlugin().(*AuditPlugin)
	if p.Name() != "audit-logger" || p.Version() == "" {
		t.Fatalf("name=%q version=%q", p.Name(), p.Version())
	}
	if len(p.ConfigSchema()) != 1 || p.ConfigSchema()[0].Key != "log_path" {
		t.Fatalf("schema = %+v", p.ConfigSchema())
	}

	if err := p.Initialize(context.Background(), map[string]interface{}{"log_path": path}); err != nil {
		t.Fatal(err)
	}
	ctx := &plugin.HookContext{UserID: "ada", MachineID: "lab", SessionID: "s1", Data: map[string]interface{}{"via": "test"}}
	hooks := []plugin.Hook{
		plugin.HookPreAuth, plugin.HookPostAuth, plugin.HookSessionStart, plugin.HookSessionEnd,
		plugin.HookAccessRequest, plugin.HookAccessGranted, plugin.HookPreConnect, plugin.HookPostConnect,
		plugin.HookPreDisconnect, plugin.HookPostDisconnect, plugin.Hook("unknown"),
	}
	for _, hook := range hooks {
		if err := p.OnHook(context.Background(), hook, ctx); err != nil {
			t.Fatalf("%s: %v", hook, err)
		}
	}
	if err := p.Shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}

	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	text := string(body)
	for _, want := range []string{"PLUGIN_INIT", "PRE_AUTH", "POST_AUTH", "SESSION_START", "SESSION_END", "ACCESS_REQUEST", "ACCESS_GRANTED", "PRE_CONNECT", "POST_CONNECT", "PRE_DISCONNECT", "POST_DISCONNECT", "PLUGIN_SHUTDOWN", "via:test"} {
		if !strings.Contains(text, want) {
			t.Errorf("log missing %q\n%s", want, text)
		}
	}
}

func TestAuditInitializeRejectsUnwritablePath(t *testing.T) {
	file := filepath.Join(t.TempDir(), "not-a-directory")
	if err := os.WriteFile(file, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	p := NewPlugin().(*AuditPlugin)
	err := p.Initialize(context.Background(), map[string]interface{}{"log_path": filepath.Join(file, "audit.log")})
	if err == nil {
		t.Fatal("Initialize accepted a path under a file")
	}
}

func TestAuditDisabledAndMissingFile(t *testing.T) {
	p := NewPlugin().(*AuditPlugin)
	p.enabled = false
	if err := p.OnHook(context.Background(), plugin.HookPreAuth, &plugin.HookContext{UserID: "a"}); err != nil {
		t.Fatal(err)
	}
	p.enabled = true
	p.writeLog("ORPHAN", "no file yet", &plugin.HookContext{Data: map[string]interface{}{"x": 1}})
}

func TestAuditInitializeRejectsRelativePath(t *testing.T) {
	p := NewPlugin().(*AuditPlugin)
	if err := p.Initialize(context.Background(), map[string]interface{}{"log_path": "audit.log"}); err == nil {
		t.Fatal("Initialize accepted a relative log_path")
	}
}
