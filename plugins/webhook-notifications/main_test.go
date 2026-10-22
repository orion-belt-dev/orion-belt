package webhooknotifications

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/orion-belt-dev/orion-belt/pkg/plugin"
)

func TestWebhookPluginMetadata(t *testing.T) {
	p := NewPlugin().(*WebhookPlugin)
	if p.Name() != "webhook-notifications" || p.Version() == "" {
		t.Fatalf("name=%q version=%q", p.Name(), p.Version())
	}
	schema := p.ConfigSchema()
	if len(schema) != 2 || schema[0].Key != "url" || !schema[0].Required {
		t.Fatalf("schema = %+v", schema)
	}
}

func TestWebhookInitializeRequiresURL(t *testing.T) {
	p := NewPlugin().(*WebhookPlugin)
	if err := p.Initialize(context.Background(), map[string]interface{}{}); err == nil {
		t.Fatal("Initialize without url succeeded")
	}
	if err := p.Initialize(context.Background(), map[string]interface{}{"url": "http://example"}); err != nil {
		t.Fatal(err)
	}
	if !p.enabled {
		t.Fatal("enabled defaulted to false")
	}
	if err := p.Shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestWebhookPostsKnownHooksAndSkipsTheRest(t *testing.T) {
	var got string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		got = string(body)
		if r.Header.Get("Content-Type") != "application/json" {
			t.Errorf("content-type %q", r.Header.Get("Content-Type"))
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(srv.Close)

	p := NewPlugin().(*WebhookPlugin)
	if err := p.Initialize(context.Background(), map[string]interface{}{
		"url": srv.URL, "enabled": true,
	}); err != nil {
		t.Fatal(err)
	}
	ctx := &plugin.HookContext{UserID: "u", MachineID: "m", SessionID: "s", Data: map[string]interface{}{"k": "v"}}
	for _, hook := range []plugin.Hook{
		plugin.HookSessionStart, plugin.HookSessionEnd, plugin.HookAccessRequest,
		plugin.HookAccessGranted, plugin.HookPostAuth,
	} {
		if err := p.OnHook(context.Background(), hook, ctx); err != nil {
			t.Fatalf("%s: %v", hook, err)
		}
		if !strings.Contains(got, string(hook)) || !strings.Contains(got, `"user_id":"u"`) {
			t.Fatalf("%s body = %s", hook, got)
		}
	}
	if err := p.OnHook(context.Background(), plugin.HookPreAuth, ctx); err != nil {
		t.Fatal(err)
	}
}

func TestWebhookDisabledAndFailures(t *testing.T) {
	p := NewPlugin().(*WebhookPlugin)
	p.enabled = false
	p.url = "http://127.0.0.1:1"
	if err := p.OnHook(context.Background(), plugin.HookSessionStart, &plugin.HookContext{}); err != nil {
		t.Fatal(err)
	}

	p.enabled = true
	if err := p.OnHook(context.Background(), plugin.HookSessionStart, &plugin.HookContext{}); err == nil {
		t.Fatal("post to a closed port succeeded")
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
	}))
	t.Cleanup(srv.Close)
	p.url = srv.URL
	err := p.OnHook(context.Background(), plugin.HookSessionStart, &plugin.HookContext{})
	if err == nil || !strings.Contains(err.Error(), "502") {
		t.Fatalf("status error = %v", err)
	}
}
