package emailnotifications

import (
	"context"
	"fmt"
	"log"
	"net/smtp"
	"strings"

	"github.com/orion-belt-dev/orion-belt/pkg/plugin"
)

// EmailPlugin sends SMTP notifications for Orion Belt events.
type EmailPlugin struct {
	name     string
	version  string
	enabled  bool
	host     string
	port     int
	username string
	password string
	from     string
	to       []string
}

// NewPlugin is the plugin loader entrypoint.
func NewPlugin() plugin.Plugin {
	return &EmailPlugin{
		name:    "email-notifications",
		version: "1.0.0",
		port:    587,
	}
}

func (p *EmailPlugin) Name() string    { return p.name }
func (p *EmailPlugin) Version() string { return p.version }

func (p *EmailPlugin) ConfigSchema() []plugin.ConfigField {
	return []plugin.ConfigField{
		{Key: "smtp_host", Label: "SMTP host", Type: "string", Required: true, Placeholder: "smtp.example.com"},
		{Key: "smtp_port", Label: "SMTP port", Type: "int", Placeholder: "587"},
		{Key: "username", Label: "Username", Type: "string"},
		{Key: "password", Label: "Password", Type: "string", Secret: true},
		{Key: "from", Label: "From address", Type: "string", Placeholder: "orion-belt@example.com"},
		{
			Key:         "to",
			Label:       "Recipients",
			Type:        "string",
			Required:    true,
			Placeholder: "admins@example.com, security@example.com",
			Help:        "Comma-separated list of recipient addresses.",
		},
		{Key: "enabled", Label: "Enabled", Type: "bool"},
	}
}

func (p *EmailPlugin) Initialize(ctx context.Context, config map[string]interface{}) error {
	if host, ok := config["smtp_host"].(string); ok {
		p.host = host
	} else {
		return fmt.Errorf("smtp_host is required")
	}
	if port, ok := config["smtp_port"].(int); ok {
		p.port = port
	}
	if v, ok := config["smtp_port"].(float64); ok {
		p.port = int(v)
	}
	if u, ok := config["username"].(string); ok {
		p.username = u
	}
	if pw, ok := config["password"].(string); ok {
		p.password = pw
	}
	if from, ok := config["from"].(string); ok {
		p.from = from
	} else {
		p.from = "orion-belt@localhost"
	}
	if to, ok := config["to"].([]interface{}); ok {
		for _, t := range to {
			if s, ok := t.(string); ok {
				p.to = append(p.to, s)
			}
		}
	}
	if toStr, ok := config["to"].(string); ok {
		for _, part := range strings.Split(toStr, ",") {
			if s := strings.TrimSpace(part); s != "" {
				p.to = append(p.to, s)
			}
		}
	}
	if enabled, ok := config["enabled"].(bool); ok {
		p.enabled = enabled
	} else {
		p.enabled = true
	}
	if len(p.to) == 0 {
		return fmt.Errorf("at least one recipient (to) is required")
	}
	log.Printf("[EmailPlugin] Initialized SMTP %s:%d enabled=%v", p.host, p.port, p.enabled)
	return nil
}

func (p *EmailPlugin) Shutdown(ctx context.Context) error {
	log.Printf("[EmailPlugin] Shutdown complete")
	return nil
}

func (p *EmailPlugin) OnHook(ctx context.Context, hook plugin.Hook, hookCtx *plugin.HookContext) error {
	if !p.enabled {
		return nil
	}
	switch hook {
	case plugin.HookSessionStart:
		return p.send("Session started", fmt.Sprintf("User %s started a session on %s (session %s)",
			hookCtx.UserID, hookCtx.MachineID, hookCtx.SessionID))
	case plugin.HookSessionEnd:
		return p.send("Session ended", fmt.Sprintf("User %s ended session %s on %s",
			hookCtx.UserID, hookCtx.SessionID, hookCtx.MachineID))
	case plugin.HookAccessRequest:
		reason, _ := hookCtx.Data["reason"].(string)
		return p.send("Access request", fmt.Sprintf("User %s requested access to %s\nReason: %s",
			hookCtx.UserID, hookCtx.MachineID, reason))
	case plugin.HookAccessGranted:
		return p.send("Access granted", fmt.Sprintf("Access granted for user %s to machine %s",
			hookCtx.UserID, hookCtx.MachineID))
	}
	return nil
}

func (p *EmailPlugin) send(subject, body string) error {
	addr := fmt.Sprintf("%s:%d", p.host, p.port)
	msg := []byte(fmt.Sprintf("From: %s\r\nTo: %s\r\nSubject: [Orion-Belt] %s\r\n\r\n%s\r\n",
		p.from, strings.Join(p.to, ","), subject, body))

	var auth smtp.Auth
	if p.username != "" {
		auth = smtp.PlainAuth("", p.username, p.password, p.host)
	}

	if err := smtp.SendMail(addr, auth, p.from, p.to, msg); err != nil {
		return fmt.Errorf("smtp send: %w", err)
	}
	log.Printf("[EmailPlugin] Sent: %s", subject)
	return nil
}

var _ plugin.Plugin = (*EmailPlugin)(nil)
var _ plugin.HookPlugin = (*EmailPlugin)(nil)
