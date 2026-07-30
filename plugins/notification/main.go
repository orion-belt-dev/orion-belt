package notification

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

type SlackPlugin struct {
	name       string
	version    string
	webhookURL string
	channel    string
	enabled    bool
	httpClient *http.Client
}

func NewPlugin() plugin.Plugin {
	return &SlackPlugin{
		name:    "slack-notifications",
		version: "1.0.0",
		httpClient: &http.Client{
			Timeout: 10 * time.Second,
		},
	}
}

func (p *SlackPlugin) Name() string {
	return p.name
}

func (p *SlackPlugin) Version() string {
	return p.version
}

func (p *SlackPlugin) ConfigSchema() []plugin.ConfigField {
	return []plugin.ConfigField{
		{
			Key:         "webhook_url",
			Label:       "Incoming webhook URL",
			Type:        "string",
			Secret:      true,
			Required:    true,
			Placeholder: "https://hooks.slack.com/services/…",
		},
		{Key: "channel", Label: "Channel", Type: "string", Placeholder: "#security"},
		{Key: "enabled", Label: "Enabled", Type: "bool"},
	}
}

func (p *SlackPlugin) Initialize(ctx context.Context, config map[string]interface{}) error {
	// Get webhook URL
	if url, ok := config["webhook_url"].(string); ok {
		p.webhookURL = url
	} else {
		return fmt.Errorf("webhook_url is required")
	}

	// Get channel
	if channel, ok := config["channel"].(string); ok {
		p.channel = channel
	}

	// Check if enabled
	if enabled, ok := config["enabled"].(bool); ok {
		p.enabled = enabled
	} else {
		p.enabled = true
	}

	log.Printf("[SlackPlugin] Initialized - Channel: %s, Enabled: %v", p.channel, p.enabled)
	if p.enabled {
		p.sendMessage("Orion-Belt Slack notifications enabled", "info")
	}

	return nil
}

func (p *SlackPlugin) Shutdown(ctx context.Context) error {
	if p.enabled {
		p.sendMessage("Orion-Belt shutting down", "warning")
	}
	log.Printf("[SlackPlugin] Shutdown complete")
	return nil
}

func (p *SlackPlugin) OnHook(ctx context.Context, hook plugin.Hook, hookCtx *plugin.HookContext) error {
	if !p.enabled {
		return nil
	}

	switch hook {
	case plugin.HookSessionStart:
		return p.onSessionStart(hookCtx)

	case plugin.HookSessionEnd:
		return p.onSessionEnd(hookCtx)

	case plugin.HookAccessRequest:
		return p.onAccessRequest(hookCtx)

	case plugin.HookAccessGranted:
		return p.onAccessGranted(hookCtx)

	case plugin.HookPostAuth:
		return p.onPostAuth(hookCtx)
	}

	return nil
}

func (p *SlackPlugin) onSessionStart(hookCtx *plugin.HookContext) error {
	message := fmt.Sprintf("*Session Started*\nUser: `%s`\nMachine: `%s`\nSession ID: `%s`",
		hookCtx.UserID,
		hookCtx.MachineID,
		hookCtx.SessionID,
	)
	return p.sendMessage(message, "info")
}

func (p *SlackPlugin) onSessionEnd(hookCtx *plugin.HookContext) error {
	message := fmt.Sprintf("*Session Ended*\nUser: `%s`\nMachine: `%s`\nSession ID: `%s`",
		hookCtx.UserID,
		hookCtx.MachineID,
		hookCtx.SessionID,
	)
	return p.sendMessage(message, "good")
}

func (p *SlackPlugin) onAccessRequest(hookCtx *plugin.HookContext) error {
	reason := ""
	if r, ok := hookCtx.Data["reason"].(string); ok {
		reason = r
	}

	message := fmt.Sprintf("*Access Request*\nUser: `%s`\nMachine: `%s`\nReason: %s\n\n_Requires admin approval_",
		hookCtx.UserID,
		hookCtx.MachineID,
		reason,
	)
	return p.sendMessage(message, "warning")
}

func (p *SlackPlugin) onAccessGranted(hookCtx *plugin.HookContext) error {
	ttl := "unlimited"
	if d, ok := hookCtx.Data["duration"].(int); ok && d > 0 {
		ttl = (time.Duration(d) * time.Second).String()
	}
	message := fmt.Sprintf("*Access Granted*\nUser: `%s`\nMachine: `%s`\nTTL: %s",
		hookCtx.UserID,
		hookCtx.MachineID,
		ttl,
	)
	return p.sendMessage(message, "good")
}

func (p *SlackPlugin) onPostAuth(hookCtx *plugin.HookContext) error {
	message := fmt.Sprintf("*User Authentication*\nUser: `%s` authenticated successfully",
		hookCtx.UserID,
	)
	log.Printf("[SlackPlugin] %s", message)
	return nil
}

func (p *SlackPlugin) sendMessage(text, color string) error {
	payload := map[string]interface{}{
		"text": text,
	}

	if p.channel != "" {
		payload["channel"] = p.channel
	}

	if color != "" {
		payload["attachments"] = []map[string]interface{}{
			{
				"color":  color,
				"text":   text,
				"footer": "Orion-Belt",
				"ts":     time.Now().Unix(),
			},
		}
	}

	jsonData, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("failed to marshal JSON: %w", err)
	}

	resp, err := p.httpClient.Post(p.webhookURL, "application/json", bytes.NewBuffer(jsonData))
	if err != nil {
		return fmt.Errorf("failed to send Slack message: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("Slack API returned status: %d", resp.StatusCode)
	}

	return nil
}

var _ plugin.Plugin = (*SlackPlugin)(nil)
var _ plugin.HookPlugin = (*SlackPlugin)(nil)
