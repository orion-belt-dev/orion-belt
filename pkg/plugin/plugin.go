package plugin

import (
	"context"
	"fmt"
	"net/http"
	"regexp"
	"sort"
	"sync"
	"time"

	"github.com/orion-belt-dev/orion-belt/pkg/common"
)

// defaultHookTimeout bounds how long a single plugin can block a hook call
// (SSH post-auth, session start/end) before it's treated as failed. Variable
// (not const) so tests can shrink it instead of sleeping for the real value.
var defaultHookTimeout = 5 * time.Second

// Plugin defines the interface for Orion-Belt plugins
type Plugin interface {
	// Name returns the plugin name
	Name() string

	// Version returns the plugin version
	Version() string

	// Initialize initializes the plugin
	Initialize(ctx context.Context, config map[string]interface{}) error

	// Shutdown cleanly shuts down the plugin
	Shutdown(ctx context.Context) error
}

// Hook defines different plugin hook points
type Hook string

const (
	// HookPreAuth is called before authentication
	HookPreAuth Hook = "pre_auth"

	// HookPostAuth is called after successful authentication
	HookPostAuth Hook = "post_auth"

	// HookPreConnect is called before establishing connection
	HookPreConnect Hook = "pre_connect"

	// HookPostConnect is called after connection is established
	HookPostConnect Hook = "post_connect"

	// HookPreDisconnect is called before disconnection
	HookPreDisconnect Hook = "pre_disconnect"

	// HookPostDisconnect is called after disconnection
	HookPostDisconnect Hook = "post_disconnect"

	// HookSessionStart is called when session starts
	HookSessionStart Hook = "session_start"

	// HookSessionEnd is called when session ends
	HookSessionEnd Hook = "session_end"

	// HookAccessRequest is called when access is requested
	HookAccessRequest Hook = "access_request"

	// HookAccessGranted is called when access is granted
	HookAccessGranted Hook = "access_granted"
)

// HookContext contains context for hook execution
type HookContext struct {
	UserID    string
	MachineID string
	SessionID string
	Data      map[string]interface{}
}

// HookPlugin defines the interface for plugins that respond to hooks
type HookPlugin interface {
	Plugin

	// OnHook is called when a hook is triggered
	OnHook(ctx context.Context, hook Hook, hookCtx *HookContext) error
}

// HTTPPlugin is implemented by plugins that need to receive inbound HTTP
// requests — e.g. chat-platform interaction callbacks for the chatops
// access-request plugin. The server mounts Handler() under
// /api/v1/public/plugins/{name}/ with that prefix stripped, and unauthenticated
// (the plugin is responsible for verifying the caller itself, e.g. via the
// chat platform's request-signing scheme).
type HTTPPlugin interface {
	Plugin
	Handler() http.Handler
}

// pluginState tracks the manager-level enabled switch and the outcome of the
// last Configure call, independent of whatever internal state the plugin
// keeps for itself. Callers must hold Manager.mu.
type pluginState struct {
	enabled    bool
	configured bool
	lastErr    string
	config     map[string]interface{}
}

// Info is the manager's public view of a plugin, used by the admin API/UI.
type Info struct {
	Name       string                 `json:"name"`
	Version    string                 `json:"version"`
	Enabled    bool                   `json:"enabled"`
	Configured bool                   `json:"configured"`
	LastError  string                 `json:"last_error,omitempty"`
	Config     map[string]interface{} `json:"config"`
	HasWebhook bool                   `json:"has_webhook"`
	Schema     []ConfigField          `json:"schema,omitempty"`
}

// ConfigField describes one field in a plugin's config, so the admin UI can
// render a real form instead of a raw JSON editor. Key is the field's name
// within its containing object (not a full dot-path) — nesting is expressed
// via Fields on a "object"-typed field, matching how chatops-access-request's
// per-platform (slack/discord/teams/rocketchat) sub-configs are shaped.
type ConfigField struct {
	Key         string        `json:"key"`
	Label       string        `json:"label"`
	Type        string        `json:"type"` // "string" | "bool" | "int" | "object"
	Secret      bool          `json:"secret,omitempty"`
	Required    bool          `json:"required,omitempty"`
	Placeholder string        `json:"placeholder,omitempty"`
	Help        string        `json:"help,omitempty"`
	Fields      []ConfigField `json:"fields,omitempty"`
}

// ConfigurablePlugin is implemented by plugins that describe their config
// shape. Its schema drives both the admin UI's form rendering and precise
// secret-field detection (on top of the name-pattern heuristic below, which
// remains the fallback for anything a schema doesn't cover).
type ConfigurablePlugin interface {
	Plugin
	ConfigSchema() []ConfigField
}

// secretConfigKey matches config keys whose values should be redacted before
// ever leaving the process (API responses, logs) — credentials and callback
// URLs for chat/webhook/SMTP plugins all land in fields shaped like these.
// This is the fallback for plugins with no ConfigSchema, and a safety net
// alongside it for anything a schema doesn't explicitly mark secret.
var secretConfigKey = regexp.MustCompile(`(?i)secret|token|password|webhook_url|api_key|signing_key`)

// secretLeafNames flattens a schema into the set of leaf field names (not
// full paths — see ConfigField) marked Secret, for O(1) lookup during
// redaction/reconciliation.
func secretLeafNames(fields []ConfigField) map[string]bool {
	if len(fields) == 0 {
		return nil
	}
	out := make(map[string]bool)
	var walk func([]ConfigField)
	walk = func(fs []ConfigField) {
		for _, f := range fs {
			if f.Secret {
				out[f.Key] = true
			}
			if len(f.Fields) > 0 {
				walk(f.Fields)
			}
		}
	}
	walk(fields)
	return out
}

// isSecretKey reports whether a config key should be treated as a secret:
// schema-declared secrets always count, name-pattern matching is the
// fallback (schemaSecrets may be nil for plugins without a schema).
func isSecretKey(key string, schemaSecrets map[string]bool) bool {
	if schemaSecrets != nil && schemaSecrets[key] {
		return true
	}
	return secretConfigKey.MatchString(key)
}

// MaskSecretValue partially reveals a secret so an operator can tell which
// credential is currently set (and spot a stale/wrong one) without the full
// value ever reaching the browser. Short values reveal nothing — a few
// characters of an 8-character secret is most of the secret.
func MaskSecretValue(v string) string {
	n := len(v)
	if n == 0 {
		return ""
	}
	if n <= 8 {
		return "********"
	}
	show := 4
	if n <= 16 {
		show = 2
	}
	return v[:show] + "****" + v[n-show:]
}

func redactConfig(config map[string]interface{}, schemaSecrets map[string]bool) map[string]interface{} {
	out := make(map[string]interface{}, len(config))
	for k, v := range config {
		if isSecretKey(k, schemaSecrets) {
			if s, ok := v.(string); ok {
				out[k] = MaskSecretValue(s)
			} else {
				out[k] = "********"
			}
			continue
		}
		out[k] = redactValue(v, schemaSecrets)
	}
	return out
}

// redactValue recurses into nested maps/slices so per-platform sub-configs
// (e.g. chatops-access-request's slack.bot_token, teams.webhook_url) get
// masked too, not just top-level keys.
func redactValue(v interface{}, schemaSecrets map[string]bool) interface{} {
	switch val := v.(type) {
	case map[string]interface{}:
		return redactConfig(val, schemaSecrets)
	case []interface{}:
		out := make([]interface{}, len(val))
		for i, item := range val {
			out[i] = redactValue(item, schemaSecrets)
		}
		return out
	default:
		return val
	}
}

// ReconcileConfig restores any secret field the caller left exactly as the
// masked placeholder we last showed them (see MaskSecretValue) back to its
// real stored value, so the UI never has to force a full retype of every
// credential just to flip one unrelated field. A field only rotates if its
// submitted value actually differs from that placeholder.
func ReconcileConfig(newConfig, oldConfig map[string]interface{}, schemaSecrets map[string]bool) map[string]interface{} {
	out := make(map[string]interface{}, len(newConfig))
	for k, v := range newConfig {
		oldV, hasOld := oldConfig[k]
		switch val := v.(type) {
		case map[string]interface{}:
			var oldSub map[string]interface{}
			if hasOld {
				oldSub, _ = oldV.(map[string]interface{})
			}
			out[k] = ReconcileConfig(val, oldSub, schemaSecrets)
		case string:
			if isSecretKey(k, schemaSecrets) && hasOld {
				if oldStr, ok := oldV.(string); ok && oldStr != "" && val == MaskSecretValue(oldStr) {
					out[k] = oldStr
					continue
				}
			}
			out[k] = val
		default:
			out[k] = v
		}
	}
	return out
}

// Manager manages plugins. Plugins are compiled directly into the server
// binary and handed to Register by the caller at startup (see
// pkg/server/plugins_builtin.go) — there is no dynamic .so loading, which
// side-steps Go plugin buildmode's CGO requirement and its strict
// same-toolchain/same-dependency-versions/same-arch/same-libc constraints.
type Manager struct {
	plugins     map[string]Plugin
	hookPlugins map[Hook][]HookPlugin
	states      map[string]*pluginState
	logger      *common.Logger
	mu          sync.RWMutex
}

// NewManager creates a new plugin manager
func NewManager(logger *common.Logger) *Manager {
	return &Manager{
		plugins:     make(map[string]Plugin),
		hookPlugins: make(map[Hook][]HookPlugin),
		states:      make(map[string]*pluginState),
		logger:      logger,
	}
}

// Register registers a plugin
func (m *Manager) Register(plugin Plugin) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	name := plugin.Name()
	if _, exists := m.plugins[name]; exists {
		return fmt.Errorf("plugin already registered: %s", name)
	}

	m.plugins[name] = plugin
	// Enabled by default (matches ship-by-default expectations); each plugin
	// still gates its own behavior internally until Configure gives it valid
	// config, so an unconfigured plugin registering here is a no-op in practice.
	m.states[name] = &pluginState{enabled: true}

	// If plugin implements HookPlugin, register for hooks
	if hookPlugin, ok := plugin.(HookPlugin); ok {
		// Register for all hooks (plugins can filter in OnHook)
		allHooks := []Hook{
			HookPreAuth, HookPostAuth, HookPreConnect, HookPostConnect,
			HookPreDisconnect, HookPostDisconnect, HookSessionStart,
			HookSessionEnd, HookAccessRequest, HookAccessGranted,
		}

		for _, hook := range allHooks {
			m.hookPlugins[hook] = append(m.hookPlugins[hook], hookPlugin)
		}
	}

	m.logger.Info("Registered plugin: %s v%s", name, plugin.Version())
	return nil
}

// Unregister unregisters a plugin
func (m *Manager) Unregister(name string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	plugin, exists := m.plugins[name]
	if !exists {
		return fmt.Errorf("plugin not found: %s", name)
	}

	delete(m.plugins, name)
	delete(m.states, name)

	// Remove from hook plugins
	if hookPlugin, ok := plugin.(HookPlugin); ok {
		for hook, plugins := range m.hookPlugins {
			for i, p := range plugins {
				if p == hookPlugin {
					m.hookPlugins[hook] = append(plugins[:i], plugins[i+1:]...)
					break
				}
			}
		}
	}

	m.logger.Info("Unregistered plugin: %s", name)
	return nil
}

// Get returns a plugin by name
func (m *Manager) Get(name string) (Plugin, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	plugin, exists := m.plugins[name]
	if !exists {
		return nil, fmt.Errorf("plugin not found: %s", name)
	}

	return plugin, nil
}

// List returns all registered plugins
func (m *Manager) List() []Plugin {
	m.mu.RLock()
	defer m.mu.RUnlock()

	plugins := make([]Plugin, 0, len(m.plugins))
	for _, plugin := range m.plugins {
		plugins = append(plugins, plugin)
	}

	return plugins
}

// TriggerHook triggers a hook and calls all registered hook plugins
func (m *Manager) TriggerHook(ctx context.Context, hook Hook, hookCtx *HookContext) error {
	m.mu.RLock()
	candidates := m.hookPlugins[hook]
	plugins := make([]HookPlugin, 0, len(candidates))
	for _, p := range candidates {
		if st, ok := m.states[p.Name()]; ok && st.enabled {
			plugins = append(plugins, p)
		}
	}
	m.mu.RUnlock()

	for _, plugin := range plugins {
		if err := m.callHook(ctx, plugin, hook, hookCtx); err != nil {
			m.logger.Error("Hook %s failed for plugin %s: %v", hook, plugin.Name(), err)
			return fmt.Errorf("hook %s failed: %w", hook, err)
		}
	}

	return nil
}

// callHook invokes a single plugin's OnHook with panic recovery and a bounded
// timeout, so one misbehaving plugin — a panic, or a blocking call with no
// timeout of its own (e.g. a webhook HTTP request) — can't crash the server
// process or stall every login/session indefinitely. On timeout the plugin's
// goroutine is abandoned (Go has no way to force-preempt it), which is an
// accepted leak for a plugin that's already misbehaving; it does not block the
// caller past defaultHookTimeout.
func (m *Manager) callHook(ctx context.Context, p HookPlugin, hook Hook, hookCtx *HookContext) error {
	done := make(chan error, 1)
	go func() {
		defer func() {
			if r := recover(); r != nil {
				m.logger.Error("Plugin %s panicked handling hook %s: %v", p.Name(), hook, r)
				done <- fmt.Errorf("plugin %s panicked: %v", p.Name(), r)
			}
		}()
		done <- p.OnHook(ctx, hook, hookCtx)
	}()

	select {
	case err := <-done:
		return err
	case <-time.After(defaultHookTimeout):
		m.logger.Error("Plugin %s timed out (>%s) handling hook %s", p.Name(), defaultHookTimeout, hook)
		return fmt.Errorf("plugin %s timed out handling hook %s", p.Name(), hook)
	case <-ctx.Done():
		return ctx.Err()
	}
}

// InitializeAll initializes all registered plugins.
func (m *Manager) InitializeAll(ctx context.Context, configs map[string]map[string]interface{}) map[string]error {
	m.mu.RLock()
	names := make([]string, 0, len(m.plugins))
	for name := range m.plugins {
		names = append(names, name)
	}
	m.mu.RUnlock()

	failed := make(map[string]error)
	for _, name := range names {
		config := configs[name]
		if config == nil {
			config = make(map[string]interface{})
		}
		if err := m.Configure(ctx, name, config); err != nil {
			failed[name] = err
		}
	}

	if len(failed) == 0 {
		return nil
	}
	return failed
}

// Configure (re-)initializes a single plugin with the given config and
// records the outcome so it shows up in Info/ListInfo. It does not change
// the plugin's manager-level enabled switch — use SetEnabled for that.
func (m *Manager) Configure(ctx context.Context, name string, config map[string]interface{}) error {
	m.mu.RLock()
	p, exists := m.plugins[name]
	m.mu.RUnlock()
	if !exists {
		return fmt.Errorf("plugin not found: %s", name)
	}

	initErr := p.Initialize(ctx, config)

	m.mu.Lock()
	st, ok := m.states[name]
	if !ok {
		st = &pluginState{enabled: true}
		m.states[name] = st
	}
	st.config = config
	if initErr != nil {
		st.configured = false
		st.lastErr = initErr.Error()
	} else {
		st.configured = true
		st.lastErr = ""
	}
	m.mu.Unlock()

	if initErr != nil {
		return fmt.Errorf("configure plugin %s: %w", name, initErr)
	}
	m.logger.Info("Configured plugin: %s", name)
	return nil
}

// SetEnabled flips the manager-level switch that gates TriggerHook and
// HTTPHandler for a plugin, independent of whether it's been configured.
// New plugins start enabled (see Register); this is the admin on/off toggle.
func (m *Manager) SetEnabled(name string, enabled bool) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, exists := m.plugins[name]; !exists {
		return fmt.Errorf("plugin not found: %s", name)
	}
	st, ok := m.states[name]
	if !ok {
		st = &pluginState{}
		m.states[name] = st
	}
	st.enabled = enabled
	return nil
}

// Info returns the manager's view of a single plugin for the admin API/UI.
func (m *Manager) Info(name string) (*Info, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	p, exists := m.plugins[name]
	if !exists {
		return nil, fmt.Errorf("plugin not found: %s", name)
	}
	return m.infoLocked(name, p), nil
}

// ListInfo returns the manager's view of every registered plugin, sorted by
// name so the UI gets a stable order across calls.
func (m *Manager) ListInfo() []*Info {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]*Info, 0, len(m.plugins))
	for name, p := range m.plugins {
		out = append(out, m.infoLocked(name, p))
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// infoLocked builds an Info snapshot. Callers must hold m.mu (read is enough).
func (m *Manager) infoLocked(name string, p Plugin) *Info {
	info := &Info{Name: name, Version: p.Version(), Config: map[string]interface{}{}}
	schemaSecrets := schemaSecretsFor(p)
	if cp, ok := p.(ConfigurablePlugin); ok {
		info.Schema = cp.ConfigSchema()
	}
	if st, ok := m.states[name]; ok {
		info.Enabled = st.enabled
		info.Configured = st.configured
		info.LastError = st.lastErr
		if st.config != nil {
			info.Config = redactConfig(st.config, schemaSecrets)
		}
	}
	_, info.HasWebhook = p.(HTTPPlugin)
	return info
}

// schemaSecretsFor returns the secret-leaf-name set for a plugin's schema, or
// nil if it doesn't implement ConfigurablePlugin (callers fall back to the
// name-pattern heuristic automatically — see isSecretKey).
func schemaSecretsFor(p Plugin) map[string]bool {
	cp, ok := p.(ConfigurablePlugin)
	if !ok {
		return nil
	}
	return secretLeafNames(cp.ConfigSchema())
}

// SchemaSecrets returns the secret-leaf-name set for a registered plugin, for
// callers (e.g. the admin API) that need to reconcile unchanged masked
// placeholders via ReconcileConfig before calling Configure.
func (m *Manager) SchemaSecrets(name string) (map[string]bool, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	p, exists := m.plugins[name]
	if !exists {
		return nil, fmt.Errorf("plugin not found: %s", name)
	}
	return schemaSecretsFor(p), nil
}

// HTTPHandler returns the inbound webhook handler for a plugin if it
// implements HTTPPlugin and is currently enabled. Disabled plugins 404
// instead of silently processing chat-platform callbacks.
func (m *Manager) HTTPHandler(name string) (http.Handler, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	st, ok := m.states[name]
	if !ok || !st.enabled {
		return nil, false
	}
	p, exists := m.plugins[name]
	if !exists {
		return nil, false
	}
	hp, ok := p.(HTTPPlugin)
	if !ok {
		return nil, false
	}
	return hp.Handler(), true
}

// ShutdownAll shuts down all registered plugins
func (m *Manager) ShutdownAll(ctx context.Context) error {
	m.mu.RLock()
	defer m.mu.RUnlock()

	for name, plugin := range m.plugins {
		if err := plugin.Shutdown(ctx); err != nil {
			m.logger.Error("Failed to shutdown plugin %s: %v", name, err)
		}
	}

	return nil
}
