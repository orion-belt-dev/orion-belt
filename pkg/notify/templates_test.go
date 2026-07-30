package notify

import (
	"strings"
	"testing"
)

func TestRenderApproved(t *testing.T) {
	title, body := Render("access_request.approved", map[string]string{
		"machine": "lab-1",
		"ttl":     "30m",
	})
	if title == "" || body == "" {
		t.Fatalf("empty render: %q %q", title, body)
	}
	if !strings.Contains(body, "lab-1") {
		t.Fatalf("expected machine in body: %q", body)
	}
}

func TestRenderRejected(t *testing.T) {
	title, body := Render("access_request.rejected", map[string]string{"machine": "x"})
	if title != "Access request rejected" || !strings.Contains(body, "x") {
		t.Fatalf("bad render: %q %q", title, body)
	}
}

// Every event type offered to admins as mandatory-notification material must
// actually render designed copy — otherwise the policy UI would let an
// operator pin an event that reaches users as a bare type string.
func TestKnownEventTypesAllRender(t *testing.T) {
	events := KnownEventTypes()
	if len(events) == 0 {
		t.Fatal("expected at least one known event type")
	}

	for _, event := range events {
		title, body := Render(event, map[string]string{"machine": "lab-1"})
		if title == "" || body == "" {
			t.Errorf("%s: empty render (title=%q body=%q)", event, title, body)
		}
		if title == event {
			t.Errorf("%s: fell through to the default branch, so it has no designed copy", event)
		}
	}
}

// The catalog is used to build UI pickers and is compared against stored
// policy keys, so its contents and order must be stable.
func TestKnownEventTypesIsStable(t *testing.T) {
	want := []string{
		EventAccessRequestApproved,
		EventAccessRequestRejected,
		EventAccessRequestExpired,
	}

	got := KnownEventTypes()
	if len(got) != len(want) {
		t.Fatalf("expected %d event types, got %v", len(want), got)
	}
	for i, event := range want {
		if got[i] != event {
			t.Errorf("index %d: expected %q, got %q", i, event, got[i])
		}
	}

	// The returned slice must not alias package state — a caller sorting or
	// appending to it would otherwise corrupt every later read.
	got[0] = "mutated"
	if KnownEventTypes()[0] != EventAccessRequestApproved {
		t.Error("KnownEventTypes returned an aliased slice; callers can corrupt the catalog")
	}
}

// An unrecognized type still has to produce something deliverable rather than
// an empty notification.
func TestRenderUnknownTypeFallsBack(t *testing.T) {
	title, body := Render("something.custom", map[string]string{"body": "custom text"})
	if title != "something.custom" {
		t.Errorf("expected the type as title, got %q", title)
	}
	if body != "custom text" {
		t.Errorf("expected the supplied body, got %q", body)
	}
}

// The derivations that used to sit inline in Render are what let an admin write
// plain copy without re-encoding every fallback, so they must survive
// templating: a missing machine name reads as its ID, remote users read as a
// phrase, and an absent expiry reads as "unlimited".
func TestRenderAppliesDerivedValues(t *testing.T) {
	_, body := Render(EventAccessRequestApproved, map[string]string{
		"machine_id":   "m-42",
		"remote_users": "root",
	})

	for _, want := range []string{"m-42", "as root", "unlimited"} {
		if !strings.Contains(body, want) {
			t.Errorf("expected %q in body, got %q", want, body)
		}
	}
}

// A remote_users value that already reads as a phrase must not be prefixed
// twice.
func TestRenderDoesNotDoublePrefixRemoteUsers(t *testing.T) {
	_, body := Render(EventAccessRequestApproved, map[string]string{
		"machine":      "lab-1",
		"remote_users": "as deploy",
	})
	if strings.Contains(body, "as as ") {
		t.Errorf("remote users prefixed twice: %q", body)
	}
}

// The point of the feature: a stored override replaces the built-in wording.
func TestTemplateSetRendersOverride(t *testing.T) {
	set := NewTemplateSet([]*Template{{
		EventType: EventAccessRequestApproved,
		Title:     "You're in",
		Body:      "{{machine}} is ready ({{ttl}}).",
	}})

	title, body := set.Render(EventAccessRequestApproved, map[string]string{
		"machine": "lab-1",
		"ttl":     "30m",
	})

	if title != "You're in" {
		t.Errorf("expected the override title, got %q", title)
	}
	if body != "lab-1 is ready (30m)." {
		t.Errorf("expected the override body with substitutions, got %q", body)
	}
}

// An override for one event must not disturb the others — a partially
// customized install still ships designed copy everywhere else.
func TestTemplateSetFallsBackToDefaults(t *testing.T) {
	set := NewTemplateSet([]*Template{{
		EventType: EventAccessRequestApproved,
		Title:     "Custom",
		Body:      "Custom body.",
	}})

	title, _ := set.Render(EventAccessRequestRejected, map[string]string{"machine": "lab-1"})
	if title != "Access request rejected" {
		t.Errorf("expected the default rejected title, got %q", title)
	}
}

// A row for an event this build does not render would be copy an admin could
// neither see nor reach, so it is dropped rather than carried around.
func TestNewTemplateSetDropsUnknownEvents(t *testing.T) {
	set := NewTemplateSet([]*Template{
		{EventType: "retired.event", Title: "x", Body: "y"},
		nil,
	})
	if len(set) != 0 {
		t.Errorf("expected unknown and nil templates to be dropped, got %v", set)
	}
}

// A nil set is the "nothing customized" state and must render the defaults
// rather than panicking or returning empty copy.
func TestNilTemplateSetRendersDefaults(t *testing.T) {
	title, body := TemplateSet(nil).Render(EventAccessRequestExpired, map[string]string{"machine": "lab-1"})
	if title != "Access request expired" || !strings.Contains(body, "lab-1") {
		t.Errorf("nil set did not render the default: %q %q", title, body)
	}
}

func TestExpandToleratesWhitespaceInPlaceholders(t *testing.T) {
	got := Expand("machine {{ machine }} ok", map[string]string{"machine": "lab-1"})
	if got != "machine lab-1 ok" {
		t.Errorf("expected whitespace inside braces to be tolerated, got %q", got)
	}
}

// A placeholder with no value is left visible rather than blanked, so copy
// that outlived its data is debuggable instead of silently truncated.
func TestExpandLeavesUnknownPlaceholders(t *testing.T) {
	got := Expand("hello {{nobody}}", map[string]string{"machine": "lab-1"})
	if got != "hello {{nobody}}" {
		t.Errorf("expected the placeholder left intact, got %q", got)
	}
}

// Expansion of a long machine name must not produce a title that Postgres
// would reject as value-too-long for notifications.title VARCHAR(255).
func TestRenderTruncatesTitleToColumnLimit(t *testing.T) {
	longMachine := strings.Repeat("m", 240)
	full := Expand("Access approved for {{machine}}", map[string]string{"machine": longMachine})
	if len([]rune(full)) <= MaxNotificationTitleLen {
		t.Fatalf("test setup: expected untruncated title over the limit, got %d", len([]rune(full)))
	}

	set := NewTemplateSet([]*Template{{
		EventType: EventAccessRequestApproved,
		Title:     "Access approved for {{machine}}",
		Body:      "ok",
	}})
	title, body := set.Render(EventAccessRequestApproved, map[string]string{
		"machine": longMachine,
	})
	if body != "ok" {
		t.Errorf("body should be untouched, got %q", body)
	}
	if n := len([]rune(title)); n != MaxNotificationTitleLen {
		t.Errorf("expected title truncated to %d runes, got %d (%q)", MaxNotificationTitleLen, n, title)
	}
	if !strings.HasPrefix(title, "Access approved for ") {
		t.Errorf("expected truncated expanded title, got %q", title)
	}
}

func TestSortTemplatesPutsUnknownEventsLast(t *testing.T) {
	templates := []*Template{
		{EventType: "stale.from.downgrade"},
		{EventType: EventAccessRequestApproved},
		{EventType: EventAccessRequestRejected},
	}
	SortTemplates(templates)
	if templates[0].EventType != EventAccessRequestApproved {
		t.Errorf("expected approved first, got %q", templates[0].EventType)
	}
	if templates[1].EventType != EventAccessRequestRejected {
		t.Errorf("expected rejected second, got %q", templates[1].EventType)
	}
	if templates[2].EventType != "stale.from.downgrade" {
		t.Errorf("expected unknown last, got %q", templates[2].EventType)
	}
}

// Validation is the only thing standing between an admin typo and a broken
// notification, since a bad placeholder renders as literal braces to a user.
func TestTemplateValidateRejectsUnknownPlaceholder(t *testing.T) {
	tmpl := &Template{
		EventType: EventAccessRequestApproved,
		Title:     "Approved",
		Body:      "Access to {{machinename}} granted.",
	}
	err := tmpl.Validate()
	if err == nil {
		t.Fatal("expected an unknown placeholder to be rejected")
	}
	if !strings.Contains(err.Error(), "machinename") {
		t.Errorf("expected the offending name in the error, got %v", err)
	}
}

// A placeholder the event does not supply must be rejected even though it is
// valid for a different event — otherwise it renders as braces.
func TestTemplateValidateRejectsPlaceholderFromAnotherEvent(t *testing.T) {
	tmpl := &Template{
		EventType: EventAccessRequestExpired,
		Title:     "Expired",
		Body:      "Request expired, access was {{ttl}}.",
	}
	if err := tmpl.Validate(); err == nil {
		t.Error("expected a placeholder outside this event's set to be rejected")
	}
}

func TestTemplateValidateRejectsEmptyAndUnknown(t *testing.T) {
	cases := []struct {
		name string
		tmpl *Template
	}{
		{"unknown event", &Template{EventType: "nope", Title: "t", Body: "b"}},
		{"empty title", &Template{EventType: EventAccessRequestApproved, Body: "b"}},
		{"empty body", &Template{EventType: EventAccessRequestApproved, Title: "t"}},
		{"long title", &Template{
			EventType: EventAccessRequestApproved,
			Title:     strings.Repeat("x", MaxTemplateTitleLen+1),
			Body:      "b",
		}},
		{"long body", &Template{
			EventType: EventAccessRequestApproved,
			Title:     "t",
			Body:      strings.Repeat("x", MaxTemplateBodyLen+1),
		}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := tc.tmpl.Validate(); err == nil {
				t.Error("expected validation to fail")
			}
		})
	}
}

func TestTemplateValidateAcceptsDefaults(t *testing.T) {
	for _, tmpl := range DefaultTemplates() {
		if err := tmpl.Validate(); err != nil {
			t.Errorf("%s: built-in copy does not validate: %v", tmpl.EventType, err)
		}
	}
}

// Whitespace around admin copy is invisible in the editor but shows up in an
// email subject line, so it is trimmed before storage.
func TestTemplateNormalizeTrims(t *testing.T) {
	tmpl := &Template{EventType: "  " + EventAccessRequestApproved + " ", Title: "  Hi  ", Body: "\nBody\n"}
	tmpl.Normalize()
	if tmpl.EventType != EventAccessRequestApproved || tmpl.Title != "Hi" || tmpl.Body != "Body" {
		t.Errorf("expected trimmed fields, got %+v", tmpl)
	}
}

// The default catalog is what a reset restores and what the UI diffs against,
// so callers must not be able to mutate it through a returned pointer.
func TestDefaultTemplateReturnsCopy(t *testing.T) {
	got := DefaultTemplate(EventAccessRequestApproved)
	got.Title = "mutated"

	if DefaultTemplate(EventAccessRequestApproved).Title == "mutated" {
		t.Error("DefaultTemplate exposed package state; a caller can corrupt the defaults")
	}
}

// Every templatable event needs a declared variable list, or the admin UI would
// offer copy with no documented way to reference the event's details.
func TestEveryKnownEventDeclaresPlaceholders(t *testing.T) {
	for _, event := range KnownEventTypes() {
		if len(PlaceholdersFor(event)) == 0 {
			t.Errorf("%s: no placeholders declared", event)
		}
	}
}

func TestPlaceholdersForReturnsCopy(t *testing.T) {
	got := PlaceholdersFor(EventAccessRequestApproved)
	got[0] = "mutated"

	if PlaceholdersFor(EventAccessRequestApproved)[0] == "mutated" {
		t.Error("PlaceholdersFor exposed package state; a caller can corrupt the catalog")
	}
}

// Default copy must only reference variables its own event supplies, otherwise
// the shipped wording would render braces to users.
func TestDefaultTemplatesOnlyUseDeclaredPlaceholders(t *testing.T) {
	for _, tmpl := range DefaultTemplates() {
		allowed := map[string]bool{}
		for _, p := range PlaceholdersFor(tmpl.EventType) {
			allowed[p] = true
		}
		for _, name := range placeholderNames(tmpl.Title + " " + tmpl.Body) {
			if !allowed[name] {
				t.Errorf("%s: default copy uses undeclared placeholder %q", tmpl.EventType, name)
			}
		}
	}
}
