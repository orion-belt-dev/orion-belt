package webhooknotifications

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"time"

	"github.com/orion-belt-dev/orion-belt/pkg/plugin"
)

// WebhookPlugin posts JSON payloads to a generic webhook URL.
type WebhookPlugin struct {
	name       string
	version    string
	enabled    bool
	url        string
	httpClient *http.Client
}

// NewPlugin is the plugin loader entrypoint.
func NewPlugin() plugin.Plugin {
	return &WebhookPlugin{
		name:    "webhook-notifications",
		version: "1.0.0",
		httpClient: &http.Client{
			Timeout: 10 * time.Second,
		},
	}
}

func (p *WebhookPlugin) Name() string    { return p.name }
func (p *WebhookPlugin) Version() string { return p.version }

func (p *WebhookPlugin) ConfigSchema() []plugin.ConfigField {
	return []plugin.ConfigField{
		{
			Key:         "url",
			Label:       "Webhook URL",
			Type:        "string",
			Secret:      true,
			Required:    true,
			Placeholder: "https://hooks.example.com/orion-belt",
			Help:        "Receives a JSON POST for session/access-request events. Treated as sensitive since these URLs are often bearer credentials in disguise.",
		},
		{Key: "enabled", Label: "Enabled", Type: "bool"},
	}
}

func (p *WebhookPlugin) Initialize(ctx context.Context, config map[string]interface{}) error {
	if url, ok := config["url"].(string); ok {
		p.url = url
	} else {
		return fmt.Errorf("url is required")
	}
	if enabled, ok := config["enabled"].(bool); ok {
		p.enabled = enabled
	} else {
		p.enabled = true
	}
	log.Printf("[WebhookPlugin] Initialized url=%s enabled=%v", p.url, p.enabled)
	return nil
}

func (p *WebhookPlugin) Shutdown(ctx context.Context) error {
	log.Printf("[WebhookPlugin] Shutdown complete")
	return nil
}

func (p *WebhookPlugin) OnHook(ctx context.Context, hook plugin.Hook, hookCtx *plugin.HookContext) error {
	if !p.enabled {
		return nil
	}
	switch hook {
	case plugin.HookSessionStart, plugin.HookSessionEnd,
		plugin.HookAccessRequest, plugin.HookAccessGranted, plugin.HookPostAuth:
		return p.post(hook, hookCtx)
	}
	return nil
}

func (p *WebhookPlugin) post(hook plugin.Hook, hookCtx *plugin.HookContext) error {
	payload := map[string]interface{}{
		"event":      string(hook),
		"user_id":    hookCtx.UserID,
		"machine_id": hookCtx.MachineID,
		"session_id": hookCtx.SessionID,
		"data":       hookCtx.Data,
		"timestamp":  time.Now().UTC().Format(time.RFC3339),
		"source":     "orion-belt",
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	resp, err := p.httpClient.Post(p.url, "application/json", bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("webhook post: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("webhook returned status %d", resp.StatusCode)
	}
	return nil
}

var _ plugin.Plugin = (*WebhookPlugin)(nil)
var _ plugin.HookPlugin = (*WebhookPlugin)(nil)
