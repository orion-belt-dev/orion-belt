package api

import (
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/orion-belt-dev/orion-belt/pkg/common"
	"github.com/orion-belt-dev/orion-belt/pkg/plugin"
)

// listPlugins returns every registered plugin and its enabled/configured
// state, so the UI can render a settings page without any server.yaml access.
func (s *APIServer) listPlugins(c *gin.Context) {
	if s.pluginManager == nil {
		c.JSON(http.StatusOK, gin.H{"plugins": []*plugin.Info{}})
		return
	}
	c.JSON(http.StatusOK, gin.H{"plugins": s.pluginManager.ListInfo()})
}

// UpdatePluginConfigRequest is the body for PUT /admin/plugins/:name/config.
type UpdatePluginConfigRequest struct {
	Enabled bool                   `json:"enabled"`
	Config  map[string]interface{} `json:"config"`
}

// updatePluginConfig reconfigures a plugin and persists the result so it
// survives a restart. The plugin's Initialize is re-run immediately, so
// changes made from the UI take effect without restarting the server.
func (s *APIServer) updatePluginConfig(c *gin.Context) {
	if s.pluginManager == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "plugins are not enabled on this server"})
		return
	}
	name := c.Param("name")
	var req UpdatePluginConfigRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if req.Config == nil {
		req.Config = map[string]interface{}{}
	}

	ctx := c.Request.Context()

	// Restore any secret field the caller left exactly as the masked
	// placeholder we last showed them (see plugin.MaskSecretValue) back to
	// its real stored value, so the UI never has to force a full retype of
	// every credential just to flip one unrelated field or the enabled
	// toggle.
	if existing, err := s.store.GetPluginSetting(ctx, name); err == nil {
		schemaSecrets, _ := s.pluginManager.SchemaSecrets(name)
		req.Config = plugin.ReconcileConfig(req.Config, existing.Config, schemaSecrets)
	}

	configErr := s.pluginManager.Configure(ctx, name, req.Config)
	if err := s.pluginManager.SetEnabled(name, req.Enabled); err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
		return
	}

	setting := &common.PluginSetting{Name: name, Enabled: req.Enabled, Config: req.Config, UpdatedAt: time.Now()}
	if err := s.store.UpsertPluginSetting(ctx, setting); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to persist plugin setting"})
		return
	}

	s.recordAudit(c, "plugin.configure", "plugin:"+name, map[string]interface{}{"enabled": req.Enabled})

	info, err := s.pluginManager.Info(name)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
		return
	}
	resp := gin.H{"plugin": info}
	if configErr != nil {
		// Setting is persisted either way — the admin can fix the config and
		// retry without losing the enabled toggle they just set.
		resp["configure_error"] = configErr.Error()
	}
	c.JSON(http.StatusOK, resp)
}

// setPluginEnabled is shared by the enable/disable convenience endpoints,
// which flip the on/off switch without touching the stored config.
func (s *APIServer) setPluginEnabled(c *gin.Context, enabled bool) {
	if s.pluginManager == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "plugins are not enabled on this server"})
		return
	}
	name := c.Param("name")
	if err := s.pluginManager.SetEnabled(name, enabled); err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
		return
	}

	ctx := c.Request.Context()
	setting, err := s.store.GetPluginSetting(ctx, name)
	if err != nil {
		// No prior config to preserve — nothing has ever been persisted for
		// this plugin, so there's nothing to clobber.
		setting = &common.PluginSetting{Name: name, Config: map[string]interface{}{}}
	}
	setting.Enabled = enabled
	setting.UpdatedAt = time.Now()
	if err := s.store.UpsertPluginSetting(ctx, setting); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to persist plugin setting"})
		return
	}

	action := "plugin.disable"
	if enabled {
		action = "plugin.enable"
	}
	s.recordAudit(c, action, "plugin:"+name, nil)

	info, _ := s.pluginManager.Info(name)
	c.JSON(http.StatusOK, gin.H{"plugin": info})
}

func (s *APIServer) enablePlugin(c *gin.Context)  { s.setPluginEnabled(c, true) }
func (s *APIServer) disablePlugin(c *gin.Context) { s.setPluginEnabled(c, false) }

// pluginWebhook proxies inbound requests (chat-platform interaction
// callbacks) to a plugin's own HTTPPlugin.Handler(), stripping the
// /api/v1/public/plugins/{name} prefix. Deliberately unauthenticated at this
// layer — the plugin is responsible for verifying the caller itself (e.g.
// Slack/Discord request signatures), the same way any public webhook receiver
// would.
func (s *APIServer) pluginWebhook(c *gin.Context) {
	if s.pluginManager == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "plugins are not enabled on this server"})
		return
	}
	name := c.Param("name")
	handler, ok := s.pluginManager.HTTPHandler(name)
	if !ok {
		c.JSON(http.StatusNotFound, gin.H{"error": "plugin not found, disabled, or has no webhook handler"})
		return
	}

	proxyPath := c.Param("proxyPath")
	if proxyPath == "" {
		proxyPath = "/"
	}
	req := c.Request.Clone(c.Request.Context())
	req.URL.Path = proxyPath
	handler.ServeHTTP(c.Writer, req)
}
