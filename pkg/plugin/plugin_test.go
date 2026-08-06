package plugin

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/orion-belt-dev/orion-belt/pkg/common"
)

type fakeHookPlugin struct {
	name string
	fn   func(ctx context.Context, hook Hook, hookCtx *HookContext) error
}

func (f *fakeHookPlugin) Name() string    { return f.name }
func (f *fakeHookPlugin) Version() string { return "0.0.1" }
func (f *fakeHookPlugin) Initialize(ctx context.Context, config map[string]interface{}) error {
	return nil
}
func (f *fakeHookPlugin) Shutdown(ctx context.Context) error { return nil }
func (f *fakeHookPlugin) OnHook(ctx context.Context, hook Hook, hookCtx *HookContext) error {
	return f.fn(ctx, hook, hookCtx)
}

func newTestManager(t *testing.T) *Manager {
	t.Helper()
	return NewManager(common.NewLogger(common.ERROR))
}

// A panicking plugin must not crash the calling goroutine/process — TriggerHook
// should recover it and surface it as a normal error.
func TestTriggerHookRecoversPluginPanic(t *testing.T) {
	m := newTestManager(t)
	p := &fakeHookPlugin{name: "panics", fn: func(ctx context.Context, hook Hook, hookCtx *HookContext) error {
		panic("boom")
	}}
	if err := m.Register(p); err != nil {
		t.Fatalf("register: %v", err)
	}

	err := m.TriggerHook(context.Background(), HookPostAuth, &HookContext{})
	if err == nil {
		t.Fatal("expected an error from a panicking plugin, got nil")
	}
	if !strings.Contains(err.Error(), "panicked") {
		t.Errorf("expected error to mention the panic, got: %v", err)
	}
}

// A plugin that blocks longer than the hook timeout must not stall the caller
// past that bound.
func TestTriggerHookTimesOutSlowPlugin(t *testing.T) {
	orig := defaultHookTimeout
	defaultHookTimeout = 30 * time.Millisecond
	defer func() { defaultHookTimeout = orig }()

	m := newTestManager(t)
	p := &fakeHookPlugin{name: "slow", fn: func(ctx context.Context, hook Hook, hookCtx *HookContext) error {
		time.Sleep(2 * time.Second)
		return nil
	}}
	if err := m.Register(p); err != nil {
		t.Fatalf("register: %v", err)
	}

	start := time.Now()
	err := m.TriggerHook(context.Background(), HookPostAuth, &HookContext{})
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("expected a timeout error from a slow plugin, got nil")
	}
	if !strings.Contains(err.Error(), "timed out") {
		t.Errorf("expected error to mention timeout, got: %v", err)
	}
	if elapsed > time.Second {
		t.Errorf("TriggerHook took %s, expected it to return near the shrunk timeout (30ms)", elapsed)
	}
}

// A well-behaved plugin's own error should still propagate normally.
func TestTriggerHookPropagatesPluginError(t *testing.T) {
	m := newTestManager(t)
	wantErr := errors.New("denied")
	p := &fakeHookPlugin{name: "erroring", fn: func(ctx context.Context, hook Hook, hookCtx *HookContext) error {
		return wantErr
	}}
	if err := m.Register(p); err != nil {
		t.Fatalf("register: %v", err)
	}

	err := m.TriggerHook(context.Background(), HookPostAuth, &HookContext{})
	if err == nil || !strings.Contains(err.Error(), "denied") {
		t.Errorf("expected wrapped %q error, got: %v", wantErr, err)
	}
}

func TestTriggerHookSuccess(t *testing.T) {
	m := newTestManager(t)
	called := false
	p := &fakeHookPlugin{name: "ok", fn: func(ctx context.Context, hook Hook, hookCtx *HookContext) error {
		called = true
		return nil
	}}
	if err := m.Register(p); err != nil {
		t.Fatalf("register: %v", err)
	}

	if err := m.TriggerHook(context.Background(), HookPostAuth, &HookContext{}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !called {
		t.Error("expected plugin OnHook to be called")
	}
}

func TestMaskSecretValue(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"", ""},
		{"short12", "********"},                    // 7 chars, <= 8
		{"exactly8", "********"},                   // 8 chars, boundary
		{"nine12345", "ni****45"},                  // 9 chars -> show=2
		{"sixteencharslong", "si****ng"},           // 16 chars -> show=2
		{"xoxb-1234567890-abcdef", "xoxb****cdef"}, // >16 -> show=4
	}
	for _, tc := range cases {
		if got := MaskSecretValue(tc.in); got != tc.want {
			t.Errorf("MaskSecretValue(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestReconcileConfigRestoresUnchangedSecret(t *testing.T) {
	old := map[string]interface{}{
		"webhook_url": "https://hooks.slack.com/services/T00/B00/verylongsecretpath",
		"channel":     "#security",
	}
	masked := MaskSecretValue(old["webhook_url"].(string))

	// Untouched: the UI echoes back exactly the masked placeholder it was shown.
	incoming := map[string]interface{}{
		"webhook_url": masked,
		"channel":     "#alerts", // a real, unrelated change
	}
	got := ReconcileConfig(incoming, old, nil)
	if got["webhook_url"] != old["webhook_url"] {
		t.Errorf("expected unchanged secret to be restored to the real value, got %v", got["webhook_url"])
	}
	if got["channel"] != "#alerts" {
		t.Errorf("expected unrelated field change to pass through, got %v", got["channel"])
	}

	// Rotated: a genuinely new value must never be clobbered back to the old one.
	rotated := map[string]interface{}{"webhook_url": "https://hooks.slack.com/services/NEW/NEW/NEW"}
	got = ReconcileConfig(rotated, old, nil)
	if got["webhook_url"] != rotated["webhook_url"] {
		t.Errorf("expected rotated secret to be kept as-is, got %v", got["webhook_url"])
	}
}

func TestReconcileConfigNested(t *testing.T) {
	old := map[string]interface{}{
		"rocketchat": map[string]interface{}{
			"webhook_url": "https://chat.example.com/hooks/abcdefghijklmnopqrstuvwxyz",
			"enabled":     true,
		},
	}
	schemaSecrets := map[string]bool{"webhook_url": true}
	masked := MaskSecretValue(old["rocketchat"].(map[string]interface{})["webhook_url"].(string))

	incoming := map[string]interface{}{
		"rocketchat": map[string]interface{}{
			"webhook_url": masked,
			"enabled":     false, // toggled off
		},
	}
	got := ReconcileConfig(incoming, old, schemaSecrets)
	sub := got["rocketchat"].(map[string]interface{})
	if sub["webhook_url"] != old["rocketchat"].(map[string]interface{})["webhook_url"] {
		t.Errorf("expected nested unchanged secret to be restored, got %v", sub["webhook_url"])
	}
	if sub["enabled"] != false {
		t.Errorf("expected nested non-secret change to pass through, got %v", sub["enabled"])
	}
}
