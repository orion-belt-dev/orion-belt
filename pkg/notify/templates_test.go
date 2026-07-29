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
