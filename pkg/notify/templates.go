package notify

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"
)

// Notification event types. These are the values that appear in a user's
// event allow-list and in an admin policy's mandatory-event map.
const (
	EventAccessRequestApproved = "access_request.approved"
	EventAccessRequestRejected = "access_request.rejected"
	EventAccessRequestExpired  = "access_request.expired"
)

// Size limits for admin-supplied copy. Title is capped below the DB column so
// there is headroom for short literal titles; expansion can still overshoot
// MaxNotificationTitleLen, which Render truncates to before delivery.
const (
	MaxTemplateTitleLen     = 200
	MaxTemplateBodyLen      = 4000
	MaxNotificationTitleLen = 255 // matches notifications.title VARCHAR(255)
)

// Template is the admin-editable copy for one event type. A stored template
// overrides the built-in default for its event; events without a stored row
// keep shipping the default, so an install that never edits anything sees no
// change.
type Template struct {
	EventType string `json:"event_type"`
	Title     string `json:"title"`
	Body      string `json:"body"`
	// Zero for an event still on its built-in copy, which has never been
	// stored — omitzero keeps that out of the response rather than reporting a
	// year-1 timestamp the UI would have to special-case.
	UpdatedAt time.Time `json:"updated_at,omitzero"`
}

// defaultTemplates is the built-in copy — the wording that was hardcoded
// before templates existed. It is the fallback for any event with no stored
// override and the target of a "reset to default" from the admin UI.
var defaultTemplates = map[string]Template{
	EventAccessRequestApproved: {
		EventType: EventAccessRequestApproved,
		Title:     "Access request approved",
		Body:      "Your access request for {{machine}} ({{remote_users}}) was approved — access {{ttl}}.",
	},
	EventAccessRequestRejected: {
		EventType: EventAccessRequestRejected,
		Title:     "Access request rejected",
		Body:      "Your access request for {{machine}} ({{remote_users}}) was rejected.",
	},
	EventAccessRequestExpired: {
		EventType: EventAccessRequestExpired,
		Title:     "Access request expired",
		Body:      "Your pending access request for {{machine}} expired without approval.",
	},
}

// templatePlaceholders declares which substitutions each event supplies. It
// drives both the admin UI's variable list and validation on save, so an
// operator cannot store a placeholder that would render as literal braces.
var templatePlaceholders = map[string][]string{
	EventAccessRequestApproved: {"machine", "machine_id", "request_id", "remote_users", "ttl"},
	EventAccessRequestRejected: {"machine", "machine_id", "request_id", "remote_users"},
	EventAccessRequestExpired:  {"machine", "machine_id", "request_id"},
}

// placeholderPattern matches {{name}}, tolerating inner whitespace so copy
// pasted from the UI's variable list works whichever way it was typed.
var placeholderPattern = regexp.MustCompile(`\{\{\s*([a-zA-Z0-9_]+)\s*\}\}`)

// KnownEventTypes lists the event types this build renders, in stable order,
// so the admin UI can offer a picker instead of a free-text field. Delivery is
// not restricted to this list — an unrecognized type still renders via the
// fallback in Render — but anything outside it has no designed copy.
func KnownEventTypes() []string {
	return []string{
		EventAccessRequestApproved,
		EventAccessRequestRejected,
		EventAccessRequestExpired,
	}
}

// IsKnownEventType reports whether eventType is one this build has designed
// copy for. Only known events are templatable: a template for an event that is
// never rendered would be dead configuration an admin could not tell was dead.
func IsKnownEventType(eventType string) bool {
	_, ok := defaultTemplates[eventType]
	return ok
}

// PlaceholdersFor returns the substitutions available to eventType, in stable
// order. An unknown event has none.
func PlaceholdersFor(eventType string) []string {
	src := templatePlaceholders[eventType]
	if len(src) == 0 {
		return nil
	}
	return append([]string(nil), src...)
}

// DefaultTemplate returns the built-in copy for eventType, or nil if this build
// has none. The result is a copy: callers may edit it freely.
func DefaultTemplate(eventType string) *Template {
	t, ok := defaultTemplates[eventType]
	if !ok {
		return nil
	}
	return &t
}

// DefaultTemplates returns the built-in copy for every known event, in the
// stable order of KnownEventTypes.
func DefaultTemplates() []*Template {
	out := make([]*Template, 0, len(defaultTemplates))
	for _, event := range KnownEventTypes() {
		out = append(out, DefaultTemplate(event))
	}
	return out
}

// Normalize trims admin-supplied copy in place. Leading and trailing
// whitespace is invisible in the editor but would show up in a Slack message
// or an email subject line.
func (t *Template) Normalize() {
	if t == nil {
		return
	}
	t.EventType = strings.TrimSpace(t.EventType)
	t.Title = strings.TrimSpace(t.Title)
	t.Body = strings.TrimSpace(t.Body)
}

// Validate reports why a template cannot be stored. It is deliberately strict
// about placeholders: a typo like {{machinename}} would otherwise be accepted
// and only discovered by the user who receives the broken notification.
func (t *Template) Validate() error {
	if t == nil {
		return fmt.Errorf("template is required")
	}
	if !IsKnownEventType(t.EventType) {
		return fmt.Errorf("unknown event type %q", t.EventType)
	}
	if t.Title == "" {
		return fmt.Errorf("title is required")
	}
	if t.Body == "" {
		return fmt.Errorf("body is required")
	}
	if len(t.Title) > MaxTemplateTitleLen {
		return fmt.Errorf("title is longer than %d characters", MaxTemplateTitleLen)
	}
	if len(t.Body) > MaxTemplateBodyLen {
		return fmt.Errorf("body is longer than %d characters", MaxTemplateBodyLen)
	}

	allowed := map[string]bool{}
	for _, p := range PlaceholdersFor(t.EventType) {
		allowed[p] = true
	}
	var unknown []string
	seen := map[string]bool{}
	for _, name := range placeholderNames(t.Title + " " + t.Body) {
		if allowed[name] || seen[name] {
			continue
		}
		seen[name] = true
		unknown = append(unknown, name)
	}
	if len(unknown) > 0 {
		return fmt.Errorf("unknown placeholder(s) for %s: %s (available: %s)",
			t.EventType, strings.Join(unknown, ", "),
			strings.Join(PlaceholdersFor(t.EventType), ", "))
	}
	return nil
}

// placeholderNames returns every {{name}} appearing in text, in order.
func placeholderNames(text string) []string {
	matches := placeholderPattern.FindAllStringSubmatch(text, -1)
	if len(matches) == 0 {
		return nil
	}
	out := make([]string, 0, len(matches))
	for _, m := range matches {
		out = append(out, m[1])
	}
	return out
}

// TemplateSet is the effective copy for a render pass: stored overrides keyed
// by event type, with anything absent falling back to the built-in default.
// A nil set is valid and renders entirely from defaults.
type TemplateSet map[string]*Template

// NewTemplateSet indexes stored overrides by event type, dropping entries this
// build has no default for. Dropping rather than keeping them means a template
// left behind by a downgrade cannot resurrect copy for an event the build no
// longer understands.
func NewTemplateSet(templates []*Template) TemplateSet {
	set := make(TemplateSet, len(templates))
	for _, t := range templates {
		if t == nil || !IsKnownEventType(t.EventType) {
			continue
		}
		set[t.EventType] = t
	}
	return set
}

// Get returns the effective template for eventType — the stored override if
// there is one, otherwise the built-in default, otherwise nil.
func (ts TemplateSet) Get(eventType string) *Template {
	if t, ok := ts[eventType]; ok && t != nil {
		return t
	}
	return DefaultTemplate(eventType)
}

// Render builds title/body for notifType using this set's effective copy.
//
// An unrecognized type is still deliverable: it renders as its own type string
// with whatever body the caller supplied, matching the pre-template fallback.
//
// Titles are truncated to MaxNotificationTitleLen so a long machine name or
// remote_users list cannot push the insert past notifications.title VARCHAR(255).
func (ts TemplateSet) Render(notifType string, data map[string]string) (title, body string) {
	tmpl := ts.Get(notifType)
	if tmpl == nil {
		return truncateRunes(notifType, MaxNotificationTitleLen), data["body"]
	}
	values := renderData(data)
	return truncateRunes(Expand(tmpl.Title, values), MaxNotificationTitleLen), Expand(tmpl.Body, values)
}

// Render builds title/body for a known notification type using the built-in
// defaults. Call sites with access to stored overrides should build a
// TemplateSet and use its Render instead.
func Render(notifType string, data map[string]string) (title, body string) {
	return TemplateSet(nil).Render(notifType, data)
}

// Expand substitutes {{name}} with the matching value. Placeholders with no
// value are left as written rather than blanked: the API rejects unknown names
// on save, so braces surviving to a reader mean the template outlived the data
// that fed it, and showing that is more debuggable than a silent gap.
func Expand(text string, data map[string]string) string {
	if text == "" || !strings.Contains(text, "{{") {
		return text
	}
	return placeholderPattern.ReplaceAllStringFunc(text, func(match string) string {
		name := placeholderPattern.FindStringSubmatch(match)[1]
		if v, ok := data[name]; ok && v != "" {
			return v
		}
		return match
	})
}

// renderData derives the presentation values templates substitute. This is the
// copy-shaping that used to sit inline in Render — a machine falling back to
// its ID, remote users reading as a phrase, an absent expiry reading as
// "unlimited" — kept here so every template gets it for free and no admin has
// to encode those fallbacks in their own wording.
func renderData(data map[string]string) map[string]string {
	out := make(map[string]string, len(data)+3)
	for k, v := range data {
		out[k] = v
	}

	if out["machine"] == "" {
		out["machine"] = data["machine_id"]
	}
	if remote := out["remote_users"]; remote == "" {
		out["remote_users"] = "as allowed"
	} else if !strings.HasPrefix(remote, "as ") {
		out["remote_users"] = "as " + remote
	}
	if out["ttl"] == "" {
		out["ttl"] = "unlimited"
	}
	return out
}

// SortTemplates orders templates by the stable catalog order of
// KnownEventTypes, so a listing does not reshuffle between reads. Unknown
// event types sort after the catalog rather than colliding at index 0.
func SortTemplates(templates []*Template) {
	known := KnownEventTypes()
	order := make(map[string]int, len(known))
	for i, event := range known {
		order[event] = i
	}
	unknown := len(known)
	sort.SliceStable(templates, func(i, j int) bool {
		oi, oki := order[templates[i].EventType]
		oj, okj := order[templates[j].EventType]
		if !oki {
			oi = unknown
		}
		if !okj {
			oj = unknown
		}
		return oi < oj
	})
}

// truncateRunes shortens s to at most max Unicode code points. Postgres
// VARCHAR(n) counts characters, not bytes.
func truncateRunes(s string, max int) string {
	if max <= 0 {
		return ""
	}
	runes := []rune(s)
	if len(runes) <= max {
		return s
	}
	return string(runes[:max])
}

// FormatTTL formats an expiry for notification copy.
func FormatTTL(expiresAt *time.Time) string {
	if expiresAt == nil {
		return "unlimited"
	}
	return "until " + expiresAt.Format(time.RFC3339)
}
