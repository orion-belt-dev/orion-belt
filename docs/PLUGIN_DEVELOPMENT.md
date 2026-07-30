# Orion-Belt Plugin Development Guide

## Overview

Orion-Belt plugins are **compiled directly into the server binary** — regular
importable Go packages, not dynamically loaded `.so` files. This sidesteps Go
plugin buildmode's CGO requirement and its strict same-toolchain /
same-dependency-versions / same-arch / same-libc constraints, so
`orion-belt-server` stays a single static binary.

Enable, disable, and reconfigure any plugin at runtime from the **Plugins**
page in the web console (or the admin REST API) — no restart, no editing
`server.yaml`. Config is stored in the database (`plugin_settings` table).

## Plugin Architecture

Plugins implement `Plugin`, and optionally `HookPlugin` (react to system
events), `ConfigurablePlugin` (describe config fields so the UI renders a real
form instead of raw JSON), and `HTTPPlugin` (receive inbound webhooks, e.g.
chat-platform callbacks):

```go
type Plugin interface {
    Name() string
    Version() string
    Initialize(ctx context.Context, config map[string]interface{}) error
    Shutdown(ctx context.Context) error
}

type HookPlugin interface {
    Plugin
    OnHook(ctx context.Context, hook Hook, hookCtx *HookContext) error
}

type ConfigurablePlugin interface {
    Plugin
    ConfigSchema() []ConfigField
}

type HTTPPlugin interface {
    Plugin
    Handler() http.Handler // mounted at /api/v1/public/plugins/{name}/, unauthenticated
}
```

## Available Hooks

- `HookPreAuth` - Before user authentication
- `HookPostAuth` - After successful authentication
- `HookPreConnect` - Before establishing connection to target machine
- `HookPostConnect` - After connection established
- `HookPreDisconnect` - Before disconnection
- `HookPostDisconnect` - After disconnection
- `HookSessionStart` - When a session starts
- `HookSessionEnd` - When a session ends
- `HookAccessRequest` - When user requests access
- `HookAccessGranted` - When access is granted

## Creating a Plugin

### Step 1: Create the package

```bash
mkdir -p plugins/my-plugin
cd plugins/my-plugin
```

### Step 2: Implement the plugin

Create `main.go` (the package name just needs to be a valid, unique Go
identifier — it's imported like any other package, not built standalone):

```go
package myplugin

import (
    "context"
    "log"

    "github.com/orion-belt-dev/orion-belt/pkg/plugin"
)

type MyPlugin struct {
    name    string
    version string
    config  map[string]interface{}
}

// NewPlugin is the entry point registerBuiltinPlugins calls — no export
// magic required since this is a normal function call, not a dlopen symbol
// lookup.
func NewPlugin() plugin.Plugin {
    return &MyPlugin{
        name:    "my-plugin",
        version: "1.0.0",
    }
}

func (p *MyPlugin) Name() string    { return p.name }
func (p *MyPlugin) Version() string { return p.version }

func (p *MyPlugin) ConfigSchema() []plugin.ConfigField {
    return []plugin.ConfigField{
        {Key: "setting1", Label: "Setting one", Type: "string", Required: true},
        {Key: "setting2", Label: "Setting two", Type: "int"},
        {Key: "api_token", Label: "API token", Type: "string", Secret: true},
    }
}

func (p *MyPlugin) Initialize(ctx context.Context, config map[string]interface{}) error {
    p.config = config
    log.Printf("[%s] Initialized with config: %v", p.name, config)
    return nil
}

func (p *MyPlugin) Shutdown(ctx context.Context) error {
    log.Printf("[%s] Shutting down", p.name)
    return nil
}

// Implement HookPlugin
func (p *MyPlugin) OnHook(ctx context.Context, hook plugin.Hook, hookCtx *plugin.HookContext) error {
    switch hook {
    case plugin.HookSessionStart:
        log.Printf("[%s] Session started: user=%s, machine=%s", p.name, hookCtx.UserID, hookCtx.MachineID)
    case plugin.HookSessionEnd:
        log.Printf("[%s] Session ended: user=%s, machine=%s", p.name, hookCtx.UserID, hookCtx.MachineID)
    }
    return nil
}

var _ plugin.Plugin = (*MyPlugin)(nil)
var _ plugin.HookPlugin = (*MyPlugin)(nil)
var _ plugin.ConfigurablePlugin = (*MyPlugin)(nil)
```

`Secret: true` fields are partially redacted wherever config leaves the
process (e.g. `xoxb****9f2c`) — see `plugin.MaskSecretValue` — and the admin
API safely reconciles an unchanged masked value back to the real secret on
save, so the UI never has to force a full retype of every credential just to
change one field.

### Step 3: Register it

Add it to `registerBuiltinPlugins` in `pkg/server/plugins_builtin.go`:

```go
import myplugin "github.com/orion-belt-dev/orion-belt/plugins/my-plugin"

func registerBuiltinPlugins(m *plugin.Manager) error {
    for _, p := range []plugin.Plugin{
        auditlogger.NewPlugin(),
        // ...
        myplugin.NewPlugin(),
    } {
        if err := m.Register(p); err != nil {
            return err
        }
    }
    return nil
}
```

That's it — `go build ./cmd/server` now ships it. It shows up in the Plugins
page automatically, `enabled` by default (manager-level) but inert until
someone gives it valid config, exactly like the shipped plugins.

### Step 4: Configure it

From the web console: **Plugins → *my-plugin* → Edit config**. Or via the API:

```bash
curl -X PUT http://localhost:8080/api/v1/admin/plugins/my-plugin/config \
  -H "X-API-Key: $ADMIN_API_KEY" -H 'Content-Type: application/json' \
  -d '{"enabled": true, "config": {"setting1": "value1", "setting2": 42}}'
```

`server.yaml`'s `plugins:` block still works, but only as a one-time seed on
a deployment's first boot — after that, the database (edited via the UI/API)
is the source of truth.

## Plugin Development Best Practices

### 1. Handle configuration safely

Always check types when accessing config values — config arrives as
`map[string]interface{}` from JSON, so nested objects come through as
`map[string]interface{}` too, not typed structs (see
`plugins/chatops-access-request/config.go` for a marshal/unmarshal-through-JSON
pattern that handles nesting cleanly):

```go
func (p *MyPlugin) Initialize(ctx context.Context, config map[string]interface{}) error {
    if val, ok := config["setting"].(string); ok {
        p.setting = val
    } else {
        p.setting = "default"
    }
    return nil
}
```

### 2. Filter hooks in OnHook

```go
func (p *MyPlugin) OnHook(ctx context.Context, hook plugin.Hook, hookCtx *plugin.HookContext) error {
    switch hook {
    case plugin.HookSessionStart, plugin.HookSessionEnd:
        return p.handleSession(hookCtx)
    default:
        return nil
    }
}
```

### 3. Error handling

If a plugin returns an error from `OnHook`, the manager logs it and — for
that hook — stops calling any hook plugins after it in the chain. Don't let
one non-critical failure (a notification webhook being down, say) block
other plugins: log-and-continue internally rather than returning an error,
unless the failure is genuinely something that should halt the hook chain.

```go
func (p *MyPlugin) OnHook(ctx context.Context, hook plugin.Hook, hookCtx *plugin.HookContext) error {
    if !p.validateUser(hookCtx.UserID) {
        return fmt.Errorf("user validation failed") // critical — block
    }
    if err := p.sendNotification(); err != nil {
        log.Printf("Failed to send notification: %v", err) // non-critical — log only
    }
    return nil
}
```

### 4. Resource cleanup

```go
func (p *MyPlugin) Shutdown(ctx context.Context) error {
    if p.connection != nil {
        p.connection.Close()
    }
    return nil
}
```

## Shipped plugins

| Plugin directory | Name (`Name()`) | Purpose |
|------------------|-----------------|---------|
| `plugins/audit-logger` | `audit-logger` | File-based audit trail (enabled and configured out of the box — no credentials needed) |
| `plugins/notification` | `slack-notifications` | Slack Incoming Webhooks |
| `plugins/email-notifications` | `email-notifications` | SMTP email alerts |
| `plugins/webhook-notifications` | `webhook-notifications` | Generic JSON webhooks |
| `plugins/chatops-access-request` | `chatops-access-request` | Access-request approvals via Slack/Discord (native interactive buttons) and Teams/Rocket.Chat (signed magic links); implements `HTTPPlugin` for the inbound callbacks |

## Testing plugins

Since plugins are regular Go packages now, they're unit-testable the normal
way — no `.so` build step required for tests (see
`plugins/chatops-access-request/*_test.go`):

```go
package myplugin

import (
    "context"
    "testing"
)

func TestPluginInitialize(t *testing.T) {
    p := NewPlugin().(*MyPlugin)
    config := map[string]interface{}{"setting1": "value"}
    if err := p.Initialize(context.Background(), config); err != nil {
        t.Fatalf("Initialize failed: %v", err)
    }
}
```

For a manual end-to-end check: `go build ./cmd/server`, run it, and watch
`server.log` for `Registered plugin: <name>` / `Configured plugin: <name>` at
startup, or hit `GET /api/v1/admin/plugins` to see its live state.

## Troubleshooting

### Plugin not showing up

- Is it actually added to `registerBuiltinPlugins` in `pkg/server/plugins_builtin.go`?
- Does `Register` return an error (duplicate name)? Check server startup logs.

### Plugin registered but inert

- Check `GET /api/v1/admin/plugins` — `configured: false` with a `last_error`
  means `Initialize` rejected the current config (often just "not configured
  yet" on a fresh install for anything needing credentials).

### Hook not firing

- Verify the plugin implements `HookPlugin` (not just `Plugin`).
- Check the manager-level `enabled` switch (Plugins page, or
  `GET /api/v1/admin/plugins`) — a disabled plugin's `OnHook` is never called.
- `TriggerHook` recovers panics and enforces a timeout (`defaultHookTimeout`,
  5s) per plugin — a slow or panicking plugin shows up as a logged error, not
  a crashed server.

## Security considerations

1. **Validate all inputs** from config and hooks.
2. **Mark credentials `Secret: true`** in `ConfigSchema()` so they're redacted
   in API responses and the UI.
3. **Sanitize data** before using in external systems.
4. **Verify inbound webhook callers** yourself if implementing `HTTPPlugin` —
   the mount point is intentionally unauthenticated at the HTTP layer (chat
   platforms can't send your session/API credentials), so the plugin must
   verify the request itself (see `plugins/chatops-access-request/slack.go`'s
   HMAC signature check and `discord.go`'s Ed25519 check for real examples).
5. **Log security-relevant events** appropriately.
