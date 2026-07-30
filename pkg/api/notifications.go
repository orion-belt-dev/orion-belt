package api

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/orion-belt-dev/orion-belt/pkg/common"
	"github.com/orion-belt-dev/orion-belt/pkg/database"
	"github.com/orion-belt-dev/orion-belt/pkg/notify"
)

// listNotifications returns the authenticated user's notifications, most
// recent first. Always scoped to the caller — there is no "list everyone's
// notifications" mode, even for admins.
func (s *APIServer) listNotifications(c *gin.Context) {
	ctx := c.Request.Context()
	userID, _ := c.Get("user_id")
	uid, _ := userID.(string)

	limit := 50
	if v := c.Query("limit"); v != "" {
		if n, err := fmt.Sscanf(v, "%d", &limit); n == 1 && err == nil && limit > 0 {
			if limit > 200 {
				limit = 200
			}
		}
	}

	notifications, err := s.store.ListUserNotifications(ctx, uid, limit, 0)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if notifications == nil {
		notifications = []*common.Notification{}
	}
	c.JSON(http.StatusOK, notifications)
}

func (s *APIServer) unreadNotificationCount(c *gin.Context) {
	ctx := c.Request.Context()
	userID, _ := c.Get("user_id")
	uid, _ := userID.(string)

	count, err := s.store.CountUnreadNotifications(ctx, uid)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"unread": count})
}

func (s *APIServer) markNotificationRead(c *gin.Context) {
	ctx := c.Request.Context()
	userID, _ := c.Get("user_id")
	uid, _ := userID.(string)

	if err := s.store.MarkNotificationRead(ctx, c.Param("id"), uid); err != nil {
		if err == database.ErrNotFound {
			c.JSON(http.StatusNotFound, gin.H{"error": "notification not found"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"message": "marked read"})
}

func (s *APIServer) markAllNotificationsRead(c *gin.Context) {
	ctx := c.Request.Context()
	userID, _ := c.Get("user_id")
	uid, _ := userID.(string)

	if err := s.store.MarkAllNotificationsRead(ctx, uid); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"message": "all marked read"})
}

// notificationPrefsResponse returns the user's preferences together with the
// admin bounds they were resolved within, so a client can render which controls
// are unavailable and which are locked on without a second round trip.
type notificationPrefsResponse struct {
	*common.NotificationPrefs
	AllowedChannels []string            `json:"allowed_channels"`
	MandatoryEvents map[string][]string `json:"mandatory_events,omitempty"`
	// Adjusted reports that the submitted preferences were corrected to fit
	// the policy, so the UI can tell the user why what they saved differs
	// from what they sent.
	Adjusted bool `json:"adjusted,omitempty"`
}

func (s *APIServer) newPrefsResponse(prefs *common.NotificationPrefs, policy *common.NotificationPolicy, adjusted bool) notificationPrefsResponse {
	resp := notificationPrefsResponse{
		NotificationPrefs: prefs,
		AllowedChannels:   policy.EffectiveAllowedChannels(),
		Adjusted:          adjusted,
	}
	mandatory := map[string][]string{}
	for _, channel := range common.KnownNotificationChannels() {
		for _, event := range policy.MandatoryEventsForChannel(channel) {
			mandatory[channel] = append(mandatory[channel], event)
		}
	}
	if len(mandatory) > 0 {
		resp.MandatoryEvents = mandatory
	}
	return resp
}

func (s *APIServer) getNotificationPrefs(c *gin.Context) {
	ctx := c.Request.Context()
	uid, _ := c.Get("user_id")
	prefs, err := s.store.GetNotificationPrefs(ctx, uid.(string))
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	policy, err := s.store.GetNotificationPolicy(ctx)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	// Clamp on read as well as write: a policy tightened after these
	// preferences were saved must be reflected immediately, not only after
	// the user next saves.
	adjusted := policy.ClampPrefs(prefs)
	c.JSON(http.StatusOK, s.newPrefsResponse(prefs, policy, adjusted))
}

func (s *APIServer) putNotificationPrefs(c *gin.Context) {
	ctx := c.Request.Context()
	uid, _ := c.Get("user_id")
	var prefs common.NotificationPrefs
	if err := c.ShouldBindJSON(&prefs); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	prefs.UserID = uid.(string)

	policy, err := s.store.GetNotificationPolicy(ctx)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	// Bring the submitted preferences inside the admin bounds before storing,
	// so what is persisted is already the effective truth. Clamping rather
	// than rejecting keeps an unrelated edit from failing just because the
	// policy tightened underneath the user; the response reports what changed.
	adjusted := policy.ClampPrefs(&prefs)

	if err := s.store.UpsertNotificationPrefs(ctx, &prefs); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, s.newPrefsResponse(&prefs, policy, adjusted))
}

// getNotificationPolicy returns the admin-set boundary. Admin-only.
func (s *APIServer) getNotificationPolicy(c *gin.Context) {
	policy, err := s.store.GetNotificationPolicy(c.Request.Context())
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"policy":         policy,
		"known_channels": common.KnownNotificationChannels(),
		"known_events":   notify.KnownEventTypes(),
	})
}

// putNotificationPolicy replaces the admin-set boundary. Admin-only.
func (s *APIServer) putNotificationPolicy(c *gin.Context) {
	var policy common.NotificationPolicy
	if err := c.ShouldBindJSON(&policy); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	// Reject unknown channels outright rather than dropping them silently —
	// a typo here would otherwise look accepted while enforcing nothing.
	if bad := unknownChannels(&policy); len(bad) > 0 {
		c.JSON(http.StatusBadRequest, gin.H{
			"error":          fmt.Sprintf("unknown notification channel(s): %s", strings.Join(bad, ", ")),
			"known_channels": common.KnownNotificationChannels(),
		})
		return
	}
	policy.Normalize()

	if err := s.store.UpsertNotificationPolicy(c.Request.Context(), &policy); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, policy)
}

// unknownChannels collects every channel named anywhere in a submitted policy
// that this build does not implement, de-duplicated and in submission order.
func unknownChannels(policy *common.NotificationPolicy) []string {
	seen := map[string]bool{}
	var bad []string
	check := func(channels []string) {
		for _, ch := range channels {
			trimmed := strings.ToLower(strings.TrimSpace(ch))
			if trimmed == "" || common.IsKnownNotificationChannel(trimmed) || seen[trimmed] {
				continue
			}
			seen[trimmed] = true
			bad = append(bad, ch)
		}
	}
	check(policy.AllowedChannels)
	for _, channels := range policy.MandatoryEvents {
		check(channels)
	}
	return bad
}

// templateEntry is one row of the admin template editor: the copy in force,
// the default it can be reset to, and the substitutions available to it.
type templateEntry struct {
	*notify.Template
	// Customized reports that this event's copy is a stored override rather
	// than the built-in default, so the UI can offer a reset only where there
	// is something to reset.
	Customized   bool     `json:"customized"`
	DefaultTitle string   `json:"default_title"`
	DefaultBody  string   `json:"default_body"`
	Placeholders []string `json:"placeholders"`
}

// listNotificationTemplates returns the effective copy for every templatable
// event — stored overrides merged over the built-in defaults. Admin-only.
func (s *APIServer) listNotificationTemplates(c *gin.Context) {
	stored, err := s.store.ListNotificationTemplates(c.Request.Context())
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	set := notify.NewTemplateSet(stored)

	entries := make([]templateEntry, 0, len(notify.KnownEventTypes()))
	for _, event := range notify.KnownEventTypes() {
		def := notify.DefaultTemplate(event)
		_, customized := set[event]
		entries = append(entries, templateEntry{
			Template:     set.Get(event),
			Customized:   customized,
			DefaultTitle: def.Title,
			DefaultBody:  def.Body,
			Placeholders: notify.PlaceholdersFor(event),
		})
	}
	c.JSON(http.StatusOK, gin.H{"templates": entries})
}

// putNotificationTemplate replaces the copy for one event type. Admin-only.
// The new wording applies to the next notification delivered — templates are
// read per delivery, so nothing has to be restarted.
func (s *APIServer) putNotificationTemplate(c *gin.Context) {
	event := c.Param("event")
	var tmpl notify.Template
	if err := c.ShouldBindJSON(&tmpl); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	// The path names the event being edited; a body that disagrees is a client
	// bug, and silently trusting either one would write copy to the wrong event.
	if tmpl.EventType != "" && tmpl.EventType != event {
		c.JSON(http.StatusBadRequest, gin.H{
			"error": fmt.Sprintf("event type in body (%q) does not match the path (%q)", tmpl.EventType, event),
		})
		return
	}
	tmpl.EventType = event
	tmpl.Normalize()

	if err := tmpl.Validate(); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"error":        err.Error(),
			"known_events": notify.KnownEventTypes(),
			"placeholders": notify.PlaceholdersFor(event),
		})
		return
	}

	if err := s.store.UpsertNotificationTemplate(c.Request.Context(), &tmpl); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	def := notify.DefaultTemplate(event)
	c.JSON(http.StatusOK, templateEntry{
		Template:     &tmpl,
		Customized:   true,
		DefaultTitle: def.Title,
		DefaultBody:  def.Body,
		Placeholders: notify.PlaceholdersFor(event),
	})
}

// deleteNotificationTemplate reverts one event to its built-in copy. Admin-only.
func (s *APIServer) deleteNotificationTemplate(c *gin.Context) {
	event := c.Param("event")
	if !notify.IsKnownEventType(event) {
		c.JSON(http.StatusBadRequest, gin.H{
			"error":        fmt.Sprintf("unknown event type %q", event),
			"known_events": notify.KnownEventTypes(),
		})
		return
	}
	if err := s.store.DeleteNotificationTemplate(c.Request.Context(), event); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	def := notify.DefaultTemplate(event)
	c.JSON(http.StatusOK, templateEntry{
		Template:     def,
		Customized:   false,
		DefaultTitle: def.Title,
		DefaultBody:  def.Body,
		Placeholders: notify.PlaceholdersFor(event),
	})
}

// notificationTemplates resolves the copy in force right now. It reads through
// to the store on every delivery rather than caching at startup, which is what
// makes an admin's edit take effect on the next notification instead of the
// next restart.
//
// A lookup failure falls back to the built-in copy: delivering the default
// wording is strictly better than dropping a notification the recipient is
// waiting on.
func (s *APIServer) notificationTemplates(ctx context.Context) notify.TemplateSet {
	stored, err := s.store.ListNotificationTemplates(ctx)
	if err != nil {
		if s.logger != nil {
			s.logger.Warn("notification template lookup failed, using defaults: %v", err)
		}
		return nil
	}
	return notify.NewTemplateSet(stored)
}

func (s *APIServer) deliverNotification(ctx context.Context, userID, notifType string, data map[string]string) {
	if s.store == nil {
		return
	}
	prefs, _ := s.store.GetNotificationPrefs(ctx, userID)
	policy, err := s.store.GetNotificationPolicy(ctx)
	if err != nil {
		// Failing open here would deliver notifications a policy may forbid;
		// failing closed would drop mandated ones. Neither is safe to guess,
		// so fall back to the permissive default (pre-policy behavior) and
		// make the reason visible.
		if s.logger != nil {
			s.logger.Warn("notification policy lookup failed, using default: %v", err)
		}
		policy = common.DefaultNotificationPolicy()
	}
	if !policy.Deliver(prefs, common.ChannelInApp, notifType) {
		return
	}
	title, body := s.notificationTemplates(ctx).Render(notifType, data)
	meta := map[string]interface{}{}
	for k, v := range data {
		meta[k] = v
	}
	n := common.NewNotification(userID, notifType, title, body, meta)
	if err := s.store.CreateNotification(ctx, n); err != nil && s.logger != nil {
		s.logger.Warn("failed to create notification: %v", err)
	}
}

func (s *APIServer) notifyAccessRequestApproved(ctx context.Context, req *common.AccessRequest) {
	if req == nil {
		return
	}
	machineName := req.MachineID
	if m, err := s.store.GetMachine(ctx, req.MachineID); err == nil && m != nil {
		machineName = m.Name
	}
	s.deliverNotification(ctx, req.UserID, "access_request.approved", map[string]string{
		"machine":      machineName,
		"machine_id":   req.MachineID,
		"request_id":   req.ID,
		"remote_users": strings.Join(req.RemoteUsers, ", "),
		"ttl":          notify.FormatTTL(req.ExpiresAt),
	})
}

func (s *APIServer) notifyAccessRequestRejected(ctx context.Context, req *common.AccessRequest) {
	if req == nil {
		return
	}
	machineName := req.MachineID
	if m, err := s.store.GetMachine(ctx, req.MachineID); err == nil && m != nil {
		machineName = m.Name
	}
	s.deliverNotification(ctx, req.UserID, "access_request.rejected", map[string]string{
		"machine":      machineName,
		"machine_id":   req.MachineID,
		"request_id":   req.ID,
		"remote_users": strings.Join(req.RemoteUsers, ", "),
	})
}

// expireStaleAccessRequests marks old pending JIT requests as expired.
func (s *APIServer) expireStaleAccessRequests(ctx context.Context) {
	n, err := s.store.ExpireStalePendingAccessRequests(ctx, 7*24*time.Hour)
	if err != nil {
		if s.logger != nil {
			s.logger.Warn("expire access requests: %v", err)
		}
		return
	}
	if n > 0 && s.logger != nil {
		s.logger.Info("Expired %d stale pending access requests", n)
	}
}
