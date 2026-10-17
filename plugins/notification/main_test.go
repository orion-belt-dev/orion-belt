package notification

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/orion-belt-dev/orion-belt/pkg/plugin"
)

func newSlack(t *testing.T, status int) (*SlackPlugin, *string) {
	t.Helper()
	var body string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		body = string(b)
		w.WriteHeader(status)
	}))
	t.Cleanup(srv.Close)
	p := NewPlugin().(*SlackPlugin)
	if err := p.Initialize(context.Background(), map[string]interface{}{
		"webhook_url": srv.URL,
		"channel":     "#sec",
		"enabled":     true,
	}); err != nil {
		t.Fatal(err)
	}
	return p, &body
}

func TestSlackMetadataAndInit(t *testing.T) {
	p := NewPlugin().(*SlackPlugin)
	if p.Name() != "slack-notifications" || p.Version() == "" {
		t.Fatalf("name=%q version=%q", p.Name(), p.Version())
	}
	if len(p.ConfigSchema()) != 3 {
		t.Fatalf("schema len %d", len(p.ConfigSchema()))
	}
	if err := p.Initialize(context.Background(), map[string]interface{}{}); err == nil {
		t.Fatal("missing webhook_url was accepted")
	}
	p.httpClient = http.DefaultClient
	if err := p.Initialize(context.Background(), map[string]interface{}{"webhook_url": "http://127.0.0.1:1", "enabled": false}); err != nil {
		t.Fatal(err)
	}
	if p.enabled {
		t.Fatal("explicit disable was ignored")
	}
}

func TestSlackHooksAndShutdown(t *testing.T) {
	p, body := newSlack(t, http.StatusOK)
	ctx := &plugin.HookContext{
		UserID: "ada", MachineID: "lab", SessionID: "s1",
		Data: map[string]interface{}{"reason": "incident", "duration": 90},
	}
	for _, hook := range []plugin.Hook{
		plugin.HookSessionStart, plugin.HookSessionEnd, plugin.HookAccessRequest,
		plugin.HookAccessGranted, plugin.HookPostAuth, plugin.HookPreAuth,
	} {
		if err := p.OnHook(context.Background(), hook, ctx); err != nil {
			t.Fatalf("%s: %v", hook, err)
		}
	}
	if !strings.Contains(*body, "#sec") || !strings.Contains(*body, "Access Granted") {
		t.Fatalf("payload = %s", *body)
	}
	if err := p.Shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}

	p.enabled = false
	if err := p.OnHook(context.Background(), plugin.HookSessionStart, ctx); err != nil {
		t.Fatal(err)
	}
	if err := p.Shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestSlackSendFailures(t *testing.T) {
	p, _ := newSlack(t, http.StatusInternalServerError)
	err := p.OnHook(context.Background(), plugin.HookSessionStart, &plugin.HookContext{UserID: "a", MachineID: "m", SessionID: "s"})
	if err == nil || !strings.Contains(err.Error(), "500") {
		t.Fatalf("status error = %v", err)
	}

	p.webhookURL = "http://127.0.0.1:1"
	if err := p.sendMessage("down", "info"); err == nil {
		t.Fatal("send to a closed port succeeded")
	}
	if err := p.onAccessGranted(&plugin.HookContext{UserID: "a", MachineID: "m", Data: map[string]interface{}{}}); err == nil {
		t.Fatal("granted hook with unlimited ttl should still attempt delivery")
	}
}
