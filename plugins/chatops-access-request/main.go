// Package chatops implements the chatops-access-request plugin: it posts
// Orion Belt access requests to Slack, Discord, Microsoft Teams, and
// Rocket.Chat, and lets an approver act on them without opening the web UI -
// via native interactive buttons on Slack/Discord, and signed magic links on
// Teams/Rocket.Chat (see teams.go/rocketchat.go for why those two can't use
// native buttons without a full bot registration).
package chatops

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"sync"
	"time"

	"github.com/orion-belt-dev/orion-belt/pkg/plugin"
)

// ChatOpsPlugin is the plugin entry point implementing plugin.Plugin,
// plugin.HookPlugin, and plugin.HTTPPlugin.
type ChatOpsPlugin struct {
	name    string
	version string

	mu  sync.RWMutex
	cfg *Config
	api *apiClient

	httpClient *http.Client
}

// NewPlugin is the loader entrypoint: `plugin.Open(path).Lookup("NewPlugin")`
// type-asserts this to `func() plugin.Plugin`, so the name and signature must
// match exactly.
func NewPlugin() plugin.Plugin {
	return &ChatOpsPlugin{
		name:    "chatops-access-request",
		version: "1.0.0",
		httpClient: &http.Client{
			Timeout: 10 * time.Second,
		},
	}
}

func (p *ChatOpsPlugin) Name() string    { return p.name }
func (p *ChatOpsPlugin) Version() string { return p.version }

func (p *ChatOpsPlugin) ConfigSchema() []plugin.ConfigField {
	return []plugin.ConfigField{
		{
			Key:         "api_base_url",
			Label:       "Orion Belt API base URL",
			Type:        "string",
			Required:    true,
			Placeholder: "http://127.0.0.1:8080",
			Help:        "Used to call back into this server's own approve/reject API.",
		},
		{
			Key:      "api_key",
			Label:    "Admin API key",
			Type:     "string",
			Secret:   true,
			Required: true,
			Help:     "An API key belonging to an admin user (see API Keys), used to call the approve/reject endpoints.",
		},
		{
			Key:    "approval_secret",
			Label:  "Magic-link signing secret",
			Type:   "string",
			Secret: true,
			Help:   "Required if Teams or Rocket.Chat is enabled below — signs the Approve/Deny links sent to those platforms.",
		},
		{
			Key:         "public_base_url",
			Label:       "Public webhook base URL",
			Type:        "string",
			Placeholder: "https://orion.example.com/api/v1/public/plugins/chatops-access-request",
			Help:        "Required if Teams or Rocket.Chat is enabled — must be reachable from the internet so magic links resolve.",
		},
		{
			Key:   "slack",
			Label: "Slack",
			Type:  "object",
			Fields: []plugin.ConfigField{
				{Key: "enabled", Label: "Enabled", Type: "bool"},
				{Key: "bot_token", Label: "Bot token", Type: "string", Secret: true, Placeholder: "xoxb-…"},
				{Key: "signing_secret", Label: "Signing secret", Type: "string", Secret: true},
				{Key: "channel", Label: "Channel ID", Type: "string", Placeholder: "C0123ABC"},
			},
		},
		{
			Key:   "discord",
			Label: "Discord",
			Type:  "object",
			Fields: []plugin.ConfigField{
				{Key: "enabled", Label: "Enabled", Type: "bool"},
				{Key: "bot_token", Label: "Bot token", Type: "string", Secret: true},
				{Key: "public_key", Label: "Application public key (hex)", Type: "string"},
				{Key: "channel_id", Label: "Channel ID", Type: "string"},
			},
		},
		{
			Key:   "teams",
			Label: "Microsoft Teams",
			Type:  "object",
			Fields: []plugin.ConfigField{
				{Key: "enabled", Label: "Enabled", Type: "bool"},
				{
					Key: "webhook_url", Label: "Incoming webhook URL", Type: "string", Secret: true,
					Help: "Approve/Deny render as Adaptive Card links, not native buttons — see teams.go.",
				},
			},
		},
		{
			Key:   "rocketchat",
			Label: "Rocket.Chat",
			Type:  "object",
			Fields: []plugin.ConfigField{
				{Key: "enabled", Label: "Enabled", Type: "bool"},
				{
					Key: "webhook_url", Label: "Incoming webhook URL", Type: "string", Secret: true,
					Help: "Approve/Deny render as message-attachment links, not native buttons — see rocketchat.go.",
				},
			},
		},
	}
}

// Initialize parses and validates the config, per the contract described in
// the plugin brief: api_base_url/api_key are always required; at least one
// platform block must be enabled; approval_secret/public_base_url are only
// required when teams or rocketchat is enabled.
func (p *ChatOpsPlugin) Initialize(ctx context.Context, config map[string]interface{}) error {
	cfg, err := parseConfig(config)
	if err != nil {
		return fmt.Errorf("chatops-access-request: %w", err)
	}

	p.mu.Lock()
	p.cfg = cfg
	p.api = newAPIClient(cfg.APIBaseURL, cfg.APIKey)
	p.mu.Unlock()

	log.Printf("[ChatOpsPlugin] Initialized (slack=%v discord=%v teams=%v rocketchat=%v)",
		cfg.Slack.Enabled, cfg.Discord.Enabled, cfg.Teams.Enabled, cfg.RocketChat.Enabled)
	return nil
}

func (p *ChatOpsPlugin) Shutdown(ctx context.Context) error {
	log.Printf("[ChatOpsPlugin] Shutdown complete")
	return nil
}

func (p *ChatOpsPlugin) config() *Config {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.cfg
}

// OnHook only reacts to HookAccessRequest. Posting is best-effort per
// configured platform: a single platform's failure is logged and does not
// stop the others, and OnHook only returns an error if every configured
// platform failed (or none were configured/enabled), so a chat-platform
// hiccup never aborts the manager's TriggerHook chain for the remaining
// plugins.
func (p *ChatOpsPlugin) OnHook(ctx context.Context, hook plugin.Hook, hookCtx *plugin.HookContext) error {
	if hook != plugin.HookAccessRequest {
		return nil
	}

	cfg := p.config()
	if cfg == nil {
		return fmt.Errorf("chatops-access-request: not configured")
	}

	requestID, _ := hookCtx.Data["request_id"].(string)
	if requestID == "" {
		return fmt.Errorf("chatops-access-request: missing request_id in hook data")
	}

	text := p.buildMessage(ctx, hookCtx, requestID)

	type attempt struct {
		platform string
		enabled  bool
		post     func() error
	}
	attempts := []attempt{
		{"slack", cfg.Slack.Enabled, func() error { return p.postSlack(ctx, text, requestID) }},
		{"discord", cfg.Discord.Enabled, func() error { return p.postDiscord(ctx, text, requestID) }},
		{"teams", cfg.Teams.Enabled, func() error { return p.postTeams(ctx, text, requestID) }},
		{"rocketchat", cfg.RocketChat.Enabled, func() error { return p.postRocketChat(ctx, text, requestID) }},
	}

	configuredCount := 0
	failureCount := 0
	for _, a := range attempts {
		if !a.enabled {
			continue
		}
		configuredCount++
		if err := a.post(); err != nil {
			failureCount++
			log.Printf("[ChatOpsPlugin] failed to post access request %s to %s: %v", requestID, a.platform, err)
		}
	}

	if configuredCount == 0 {
		return fmt.Errorf("chatops-access-request: no platform configured/enabled")
	}
	if failureCount == configuredCount {
		return fmt.Errorf("chatops-access-request: all %d configured platform(s) failed to post request %s", configuredCount, requestID)
	}
	return nil
}

// buildMessage renders the human-readable access-request text, best-effort
// enriched with the requester's username and the target machine's name via
// Orion Belt's own REST API. Enrichment failures fall back to raw IDs -
// never fail the hook over a cosmetic lookup.
func (p *ChatOpsPlugin) buildMessage(ctx context.Context, hookCtx *plugin.HookContext, requestID string) string {
	userDisplay := hookCtx.UserID
	machineDisplay := hookCtx.MachineID

	// Bound enrichment lookups so a slow/unreachable core API can't block
	// message posting for long.
	enrichCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()

	if username, err := p.api.GetUsername(enrichCtx, hookCtx.UserID); err == nil {
		userDisplay = username
	}
	if machineName, err := p.api.GetMachineName(enrichCtx, hookCtx.MachineID); err == nil {
		machineDisplay = machineName
	}

	reason, _ := hookCtx.Data["reason"].(string)
	durationSeconds := 0
	switch v := hookCtx.Data["duration"].(type) {
	case int:
		durationSeconds = v
	case float64:
		durationSeconds = int(v)
	}

	msg := fmt.Sprintf("Access request from *%s* for machine *%s*\nRequest ID: `%s`", userDisplay, machineDisplay, requestID)
	if reason != "" {
		msg += fmt.Sprintf("\nReason: %s", reason)
	}
	if durationSeconds > 0 {
		msg += fmt.Sprintf("\nDuration: %s", time.Duration(durationSeconds)*time.Second)
	}
	return msg
}

// Handler returns the HTTP routes this plugin exposes, mounted by the server
// under /api/v1/public/plugins/chatops-access-request/ with that prefix
// stripped - so routes are registered relative to root here.
func (p *ChatOpsPlugin) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/slack/interactions", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		cfg := p.config()
		if cfg == nil || !cfg.Slack.Enabled {
			http.Error(w, "slack not configured", http.StatusNotFound)
			return
		}
		p.handleSlackInteraction(w, r)
	})
	mux.HandleFunc("/discord/interactions", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		cfg := p.config()
		if cfg == nil || !cfg.Discord.Enabled {
			http.Error(w, "discord not configured", http.StatusNotFound)
			return
		}
		p.handleDiscordInteraction(w, r)
	})
	mux.HandleFunc("/approve", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		cfg := p.config()
		if cfg == nil || cfg.ApprovalSecret == "" {
			http.Error(w, "magic links not configured", http.StatusNotFound)
			return
		}
		p.handleMagicLink("approve")(w, r)
	})
	mux.HandleFunc("/deny", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		cfg := p.config()
		if cfg == nil || cfg.ApprovalSecret == "" {
			http.Error(w, "magic links not configured", http.StatusNotFound)
			return
		}
		p.handleMagicLink("deny")(w, r)
	})
	return mux
}

var _ plugin.Plugin = (*ChatOpsPlugin)(nil)
var _ plugin.HookPlugin = (*ChatOpsPlugin)(nil)
var _ plugin.HTTPPlugin = (*ChatOpsPlugin)(nil)
