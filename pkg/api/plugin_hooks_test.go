package api

import (
	"context"
	"net/http"
	"sync"
	"testing"

	"github.com/orion-belt-dev/orion-belt/pkg/plugin"
)

// hookRecorder is a plugin that records every hook it receives.
type hookRecorder struct {
	mu    sync.Mutex
	calls []recordedHook
}

type recordedHook struct {
	hook plugin.Hook
	ctx  plugin.HookContext
}

func (r *hookRecorder) Name() string                                             { return "hook-recorder" }
func (r *hookRecorder) Version() string                                          { return "test" }
func (r *hookRecorder) Initialize(context.Context, map[string]interface{}) error { return nil }
func (r *hookRecorder) Shutdown(context.Context) error                           { return nil }
func (r *hookRecorder) OnHook(_ context.Context, hook plugin.Hook, hc *plugin.HookContext) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls = append(r.calls, recordedHook{hook, *hc})
	return nil
}

func (r *hookRecorder) received(hook plugin.Hook) []plugin.HookContext {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []plugin.HookContext
	for _, c := range r.calls {
		if c.hook == hook {
			out = append(out, c.ctx)
		}
	}
	return out
}

// The JIT path fires the plugin hooks integrations rely on, with the request
// in context, and a plugin disabled through the API stops receiving them.
func TestPluginHooksOnTheJITPath(t *testing.T) {
	e := newEnv(t, nil)
	rec := &hookRecorder{}
	if err := e.plugins.Register(rec); err != nil {
		t.Fatal(err)
	}

	req := e.requestAccess(e.aliceKey, e.machine.ID, "root", 600)
	requested := rec.received(plugin.HookAccessRequest)
	if len(requested) != 1 || requested[0].UserID != e.alice.ID || requested[0].MachineID != e.machine.ID ||
		requested[0].Data["request_id"] != req.ID {
		t.Fatalf("access_request hooks = %+v", requested)
	}

	e.expect(e.do(http.MethodPost, "/api/v1/admin/access-requests/"+req.ID+"/approve", e.adminKey, nil), http.StatusOK)
	granted := rec.received(plugin.HookAccessGranted)
	if len(granted) != 1 || granted[0].Data["request_id"] != req.ID || granted[0].Data["reviewer_id"] != e.admin.ID {
		t.Fatalf("access_granted hooks = %+v", granted)
	}

	e.expect(e.do(http.MethodPost, "/api/v1/admin/plugins/hook-recorder/disable", e.adminKey, nil), http.StatusOK)
	e.requestAccess(e.aliceKey, e.machine.ID, "deploy", 600)
	if n := len(rec.received(plugin.HookAccessRequest)); n != 1 {
		t.Errorf("a disabled plugin received %d access_request hooks, want still 1", n)
	}
}
