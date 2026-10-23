package server

import (
	"context"
	"sync"
	"testing"

	"github.com/orion-belt-dev/orion-belt/pkg/plugin"
)

type hookRecorder struct {
	mu    sync.Mutex
	hooks map[plugin.Hook][]plugin.HookContext
}

func (r *hookRecorder) Name() string                                             { return "hook-recorder" }
func (r *hookRecorder) Version() string                                          { return "test" }
func (r *hookRecorder) Initialize(context.Context, map[string]interface{}) error { return nil }
func (r *hookRecorder) Shutdown(context.Context) error                           { return nil }
func (r *hookRecorder) OnHook(_ context.Context, hook plugin.Hook, hc *plugin.HookContext) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.hooks == nil {
		r.hooks = map[plugin.Hook][]plugin.HookContext{}
	}
	r.hooks[hook] = append(r.hooks[hook], *hc)
	return nil
}

func (r *hookRecorder) received(hook plugin.Hook) []plugin.HookContext {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.hooks[hook]
}

// A session through the gateway fires post_auth, session_start and
// session_end for plugins, all naming the same user and session.
func TestGateway_PluginHooksAcrossASession(t *testing.T) {
	h := startHarness(t, nil)
	rec := &hookRecorder{}
	if err := h.srv.pluginManager.Register(rec); err != nil {
		t.Fatal(err)
	}
	h.connectAgent(t)
	h.grant(t, "root")

	if res := h.runClient(t, "alice+web-01", "uptime", ""); res.status != 5 {
		t.Fatalf("session = %+v", res)
	}

	auth := rec.received(plugin.HookPostAuth)
	if len(auth) == 0 || auth[len(auth)-1].UserID != h.alice.ID {
		t.Errorf("post_auth hooks = %+v", auth)
	}
	start, end := rec.received(plugin.HookSessionStart), rec.received(plugin.HookSessionEnd)
	if len(start) != 1 || len(end) != 1 {
		t.Fatalf("session hooks: start=%+v end=%+v", start, end)
	}
	if start[0].SessionID == "" || start[0].SessionID != end[0].SessionID ||
		start[0].UserID != h.alice.ID || start[0].MachineID != h.machine.ID {
		t.Errorf("session hook contexts: start=%+v end=%+v", start[0], end[0])
	}
}
