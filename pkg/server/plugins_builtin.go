package server

import (
	"github.com/orion-belt-dev/orion-belt/pkg/plugin"

	auditlogger "github.com/orion-belt-dev/orion-belt/plugins/audit-logger"
	chatops "github.com/orion-belt-dev/orion-belt/plugins/chatops-access-request"
	emailnotifications "github.com/orion-belt-dev/orion-belt/plugins/email-notifications"
	slacknotify "github.com/orion-belt-dev/orion-belt/plugins/notification"
	webhooknotifications "github.com/orion-belt-dev/orion-belt/plugins/webhook-notifications"
)

// registerBuiltinPlugins wires every bundled plugin directly into the
// manager. They're compiled into this binary — no dynamic .so loading, no
// CGO requirement, no arch/libc matching to get wrong. Each is enabled by
// default (see plugin.Manager.Register); whether it actually does anything
// depends on defaultPluginConfigs below plus whatever the operator configures
// from the Plugins settings UI, since most of them need credentials (webhook
// URLs, bot tokens, SMTP creds) that can't have a safe built-in default.
func registerBuiltinPlugins(m *plugin.Manager) error {
	for _, p := range []plugin.Plugin{
		auditlogger.NewPlugin(),
		slacknotify.NewPlugin(),
		emailnotifications.NewPlugin(),
		webhooknotifications.NewPlugin(),
		chatops.NewPlugin(),
	} {
		if err := m.Register(p); err != nil {
			return err
		}
	}
	return nil
}

// defaultPluginConfigs seeds plugin_settings (once, on first boot) for
// plugins that can run with zero operator input, so a fresh install has at
// least one working plugin out of the box instead of everything sitting
// "enabled" but inert until someone visits the UI.
var defaultPluginConfigs = map[string]map[string]interface{}{
	"audit-logger": {"log_path": "/var/log/orion-belt/audit-plugin.log"},
}
