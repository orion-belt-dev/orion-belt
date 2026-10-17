package emailnotifications

import (
	"context"
	"strings"
	"testing"

	"github.com/orion-belt-dev/orion-belt/pkg/plugin"
)

func TestEmailMetadataAndInit(t *testing.T) {
	p := NewPlugin().(*EmailPlugin)
	if p.Name() != "email-notifications" || p.Version() == "" || p.port != 587 {
		t.Fatalf("plugin = %+v", p)
	}
	if len(p.ConfigSchema()) < 5 {
		t.Fatalf("schema = %+v", p.ConfigSchema())
	}
	if err := p.Initialize(context.Background(), map[string]interface{}{}); err == nil {
		t.Fatal("missing smtp_host was accepted")
	}
	err := p.Initialize(context.Background(), map[string]interface{}{
		"smtp_host": "smtp.example",
		"smtp_port": float64(2525),
		"username":  "u",
		"password":  "p",
		"to":        "  , ",
	})
	if err == nil || !strings.Contains(err.Error(), "recipient") {
		t.Fatalf("empty recipients error = %v", err)
	}

	if err := p.Initialize(context.Background(), map[string]interface{}{
		"smtp_host": "smtp.example",
		"smtp_port": 2525,
		"from":      "orion@example",
		"to":        []interface{}{"a@example", 7, "b@example"},
		"enabled":   false,
	}); err != nil {
		t.Fatal(err)
	}
	if p.port != 2525 || p.from != "orion@example" || len(p.to) != 2 || p.enabled {
		t.Fatalf("after init: %+v", p)
	}
	if err := p.Shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestEmailHooksSkipWhenDisabledAndFailWithoutSMTP(t *testing.T) {
	p := NewPlugin().(*EmailPlugin)
	p.enabled = false
	p.host = "127.0.0.1"
	p.port = 1
	p.from = "orion@example"
	p.to = []string{"a@example"}
	ctx := &plugin.HookContext{UserID: "ada", MachineID: "lab", SessionID: "s", Data: map[string]interface{}{"reason": "fix"}}
	if err := p.OnHook(context.Background(), plugin.HookSessionStart, ctx); err != nil {
		t.Fatal(err)
	}

	p.enabled = true
	p.username = "u"
	p.password = "p"
	for _, hook := range []plugin.Hook{plugin.HookSessionStart, plugin.HookSessionEnd, plugin.HookAccessRequest, plugin.HookAccessGranted, plugin.HookPreAuth} {
		err := p.OnHook(context.Background(), hook, ctx)
		if hook == plugin.HookPreAuth {
			if err != nil {
				t.Fatalf("ignored hook returned %v", err)
			}
			continue
		}
		if err == nil || !strings.Contains(err.Error(), "smtp send") {
			t.Fatalf("%s error = %v", hook, err)
		}
	}
}
