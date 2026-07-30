package auditlogger

import (
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"time"

	"github.com/orion-belt-dev/orion-belt/pkg/plugin"
)

type AuditPlugin struct {
	name    string
	version string
	config  map[string]interface{}
	logFile *os.File
	enabled bool
}

func NewPlugin() plugin.Plugin {
	return &AuditPlugin{
		name:    "audit-logger",
		version: "1.0.0",
		enabled: true,
	}
}

func (p *AuditPlugin) Name() string {
	return p.name
}

func (p *AuditPlugin) Version() string {
	return p.version
}

func (p *AuditPlugin) ConfigSchema() []plugin.ConfigField {
	return []plugin.ConfigField{
		{
			Key:         "log_path",
			Label:       "Log file path",
			Type:        "string",
			Required:    true,
			Placeholder: "/var/log/orion-belt/audit-plugin.log",
			Help:        "Local file the plugin appends audit events to.",
		},
	}
}

func (p *AuditPlugin) Initialize(ctx context.Context, config map[string]interface{}) error {
	p.config = config

	// Get log file path from config, defaults to /var/log/orion-belt/audit.log
	logPath := "/var/log/orion-belt/audit.log"
	if path, ok := config["log_path"].(string); ok {
		logPath = path
	}

	logDir := filepath.Dir(logPath)
	if err := os.MkdirAll(logDir, 0755); err != nil {
		return fmt.Errorf("failed to create log directory %s: %w", logDir, err)
	}

	// Open log file
	file, err := os.OpenFile(logPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return fmt.Errorf("failed to open audit log file: %w", err)
	}

	p.logFile = file
	log.Printf("[AuditPlugin] Initialized with log path: %s", logPath)

	p.writeLog("PLUGIN_INIT", "Audit plugin initialized", nil)

	return nil
}

func (p *AuditPlugin) Shutdown(ctx context.Context) error {
	p.writeLog("PLUGIN_SHUTDOWN", "Audit plugin shutting down", nil)

	if p.logFile != nil {
		p.logFile.Close()
	}

	log.Printf("[AuditPlugin] Shutdown complete")
	return nil
}

func (p *AuditPlugin) OnHook(ctx context.Context, hook plugin.Hook, hookCtx *plugin.HookContext) error {
	if !p.enabled {
		return nil
	}

	switch hook {
	case plugin.HookPreAuth:
		p.writeLog("PRE_AUTH", fmt.Sprintf("User attempting authentication: %s", hookCtx.UserID), hookCtx)

	case plugin.HookPostAuth:
		p.writeLog("POST_AUTH", fmt.Sprintf("User authenticated: %s", hookCtx.UserID), hookCtx)

	case plugin.HookSessionStart:
		p.writeLog("SESSION_START",
			fmt.Sprintf("Session started - User: %s, Machine: %s, SessionID: %s",
				hookCtx.UserID, hookCtx.MachineID, hookCtx.SessionID), hookCtx)

	case plugin.HookSessionEnd:
		p.writeLog("SESSION_END",
			fmt.Sprintf("Session ended - User: %s, Machine: %s, SessionID: %s",
				hookCtx.UserID, hookCtx.MachineID, hookCtx.SessionID), hookCtx)

	case plugin.HookAccessRequest:
		p.writeLog("ACCESS_REQUEST",
			fmt.Sprintf("Access requested - User: %s, Machine: %s",
				hookCtx.UserID, hookCtx.MachineID), hookCtx)

	case plugin.HookAccessGranted:
		p.writeLog("ACCESS_GRANTED",
			fmt.Sprintf("Access granted - User: %s, Machine: %s",
				hookCtx.UserID, hookCtx.MachineID), hookCtx)

	case plugin.HookPreConnect:
		p.writeLog("PRE_CONNECT",
			fmt.Sprintf("Pre-connect - User: %s, Machine: %s",
				hookCtx.UserID, hookCtx.MachineID), hookCtx)

	case plugin.HookPostConnect:
		p.writeLog("POST_CONNECT",
			fmt.Sprintf("Post-connect - User: %s, Machine: %s",
				hookCtx.UserID, hookCtx.MachineID), hookCtx)

	case plugin.HookPreDisconnect:
		p.writeLog("PRE_DISCONNECT",
			fmt.Sprintf("Pre-disconnect - User: %s, Machine: %s, SessionID: %s",
				hookCtx.UserID, hookCtx.MachineID, hookCtx.SessionID), hookCtx)

	case plugin.HookPostDisconnect:
		p.writeLog("POST_DISCONNECT",
			fmt.Sprintf("Post-disconnect - User: %s, Machine: %s, SessionID: %s",
				hookCtx.UserID, hookCtx.MachineID, hookCtx.SessionID), hookCtx)
	}

	return nil
}

func (p *AuditPlugin) writeLog(eventType, message string, hookCtx *plugin.HookContext) {
	if p.logFile == nil {
		return
	}

	timestamp := time.Now().Format(time.RFC3339)
	logLine := fmt.Sprintf("[%s] [%s] %s", timestamp, eventType, message)

	if hookCtx != nil && len(hookCtx.Data) > 0 {
		logLine += fmt.Sprintf(" Data: %v", hookCtx.Data)
	}

	logLine += "\n"

	p.logFile.WriteString(logLine)
	p.logFile.Sync()
}

var _ plugin.Plugin = (*AuditPlugin)(nil)
var _ plugin.HookPlugin = (*AuditPlugin)(nil)
