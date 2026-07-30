package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/zrougamed/orion-belt/pkg/common"
	"github.com/zrougamed/orion-belt/pkg/database"
	"github.com/zrougamed/orion-belt/pkg/notify"
)

// templateStore is a stand-in for the parts of the store the notification path
// touches. It embeds the interface so the compiler supplies the rest: a method
// this test does not stub is one the code under test must not be calling.
type templateStore struct {
	database.Store

	templates     []*notify.Template
	templatesErr  error
	created       []*common.Notification
	upserted      *notify.Template
	deletedEvents []string
}

func (s *templateStore) ListNotificationTemplates(context.Context) ([]*notify.Template, error) {
	if s.templatesErr != nil {
		return nil, s.templatesErr
	}
	return s.templates, nil
}

func (s *templateStore) UpsertNotificationTemplate(_ context.Context, t *notify.Template) error {
	s.upserted = t
	s.templates = append(s.templates, t)
	return nil
}

func (s *templateStore) DeleteNotificationTemplate(_ context.Context, eventType string) error {
	s.deletedEvents = append(s.deletedEvents, eventType)
	kept := s.templates[:0]
	for _, t := range s.templates {
		if t.EventType != eventType {
			kept = append(kept, t)
		}
	}
	s.templates = kept
	return nil
}

func (s *templateStore) GetNotificationPrefs(_ context.Context, userID string) (*common.NotificationPrefs, error) {
	return common.DefaultNotificationPrefs(userID), nil
}

func (s *templateStore) GetNotificationPolicy(context.Context) (*common.NotificationPolicy, error) {
	return common.DefaultNotificationPolicy(), nil
}

func (s *templateStore) CreateNotification(_ context.Context, n *common.Notification) error {
	s.created = append(s.created, n)
	return nil
}

func testContext(t *testing.T, method, body string, params gin.Params) (*gin.Context, *httptest.ResponseRecorder) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(method, "/", strings.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")
	c.Params = params
	return c, rec
}

func decodeBody[T any](t *testing.T, rec *httptest.ResponseRecorder) T {
	t.Helper()
	var out T
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode response: %v (body %s)", err, rec.Body.String())
	}
	return out
}

// The headline behaviour: a stored override, not the built-in copy, is what
// reaches the recipient.
func TestDeliverNotificationUsesStoredTemplate(t *testing.T) {
	store := &templateStore{templates: []*notify.Template{{
		EventType: notify.EventAccessRequestApproved,
		Title:     "Access granted",
		Body:      "{{machine}} is yours for {{ttl}}.",
	}}}
	s := &APIServer{store: store}

	s.deliverNotification(context.Background(), "u1", notify.EventAccessRequestApproved, map[string]string{
		"machine": "lab-1",
		"ttl":     "30m",
	})

	if len(store.created) != 1 {
		t.Fatalf("expected one notification, got %d", len(store.created))
	}
	got := store.created[0]
	if got.Title != "Access granted" {
		t.Errorf("expected the override title, got %q", got.Title)
	}
	if got.Body != "lab-1 is yours for 30m." {
		t.Errorf("expected the override body, got %q", got.Body)
	}
}

// The acceptance criterion that templates apply without a restart: the same
// long-lived server must pick up an edit made after it started serving.
func TestTemplateEditAppliesToNextDeliveryWithoutRestart(t *testing.T) {
	store := &templateStore{}
	s := &APIServer{store: store}

	s.deliverNotification(context.Background(), "u1", notify.EventAccessRequestApproved,
		map[string]string{"machine": "lab-1"})

	// Edit the copy through the admin endpoint on the very same server value —
	// no reconstruction, no reload hook.
	c, rec := testContext(t, http.MethodPut,
		`{"title":"Edited title","body":"Edited: {{machine}}."}`,
		gin.Params{{Key: "event", Value: notify.EventAccessRequestApproved}})
	s.putNotificationTemplate(c)
	if rec.Code != http.StatusOK {
		t.Fatalf("save failed: %d %s", rec.Code, rec.Body.String())
	}

	s.deliverNotification(context.Background(), "u1", notify.EventAccessRequestApproved,
		map[string]string{"machine": "lab-1"})

	if len(store.created) != 2 {
		t.Fatalf("expected two notifications, got %d", len(store.created))
	}
	if store.created[0].Title != "Access request approved" {
		t.Errorf("expected the default before the edit, got %q", store.created[0].Title)
	}
	if store.created[1].Title != "Edited title" {
		t.Errorf("expected the edited title after the edit, got %q", store.created[1].Title)
	}
	if store.created[1].Body != "Edited: lab-1." {
		t.Errorf("expected the edited body after the edit, got %q", store.created[1].Body)
	}
}

// A store that cannot be read must not silently drop a notification the
// recipient is waiting on — the built-in copy still goes out.
func TestDeliverNotificationFallsBackWhenTemplateLookupFails(t *testing.T) {
	store := &templateStore{templatesErr: fmt.Errorf("db down")}
	s := &APIServer{store: store}

	s.deliverNotification(context.Background(), "u1", notify.EventAccessRequestApproved,
		map[string]string{"machine": "lab-1"})

	if len(store.created) != 1 {
		t.Fatalf("expected the notification to still be delivered, got %d", len(store.created))
	}
	if store.created[0].Title != "Access request approved" {
		t.Errorf("expected the built-in title, got %q", store.created[0].Title)
	}
}

type templateListResponse struct {
	Templates []struct {
		EventType    string   `json:"event_type"`
		Title        string   `json:"title"`
		Body         string   `json:"body"`
		Customized   bool     `json:"customized"`
		DefaultTitle string   `json:"default_title"`
		DefaultBody  string   `json:"default_body"`
		Placeholders []string `json:"placeholders"`
	} `json:"templates"`
}

// The editor needs every templatable event listed, the effective copy for each,
// and the default to diff against — otherwise it cannot show what is customized
// or offer a meaningful reset.
func TestListNotificationTemplatesMergesDefaults(t *testing.T) {
	store := &templateStore{templates: []*notify.Template{{
		EventType: notify.EventAccessRequestApproved,
		Title:     "Custom",
		Body:      "Custom {{machine}}.",
	}}}
	s := &APIServer{store: store}

	c, rec := testContext(t, http.MethodGet, "", nil)
	s.listNotificationTemplates(c)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	resp := decodeBody[templateListResponse](t, rec)
	if len(resp.Templates) != len(notify.KnownEventTypes()) {
		t.Fatalf("expected every known event listed, got %d", len(resp.Templates))
	}

	approved := resp.Templates[0]
	if approved.EventType != notify.EventAccessRequestApproved {
		t.Fatalf("expected the catalog order, got %q first", approved.EventType)
	}
	if approved.Title != "Custom" || !approved.Customized {
		t.Errorf("expected the override reported as customized, got %+v", approved)
	}
	if approved.DefaultTitle != "Access request approved" {
		t.Errorf("expected the default carried for reset, got %q", approved.DefaultTitle)
	}
	if len(approved.Placeholders) == 0 {
		t.Error("expected the available variables to be advertised")
	}

	rejected := resp.Templates[1]
	if rejected.Customized {
		t.Error("expected an untouched event to report as not customized")
	}
	if rejected.Title != rejected.DefaultTitle {
		t.Errorf("expected an untouched event to render its default, got %q", rejected.Title)
	}
}

// Bad copy must be rejected on save, where an admin can see and fix it, rather
// than at delivery where it reaches a user as literal braces.
func TestPutNotificationTemplateRejectsBadCopy(t *testing.T) {
	cases := []struct {
		name  string
		event string
		body  string
	}{
		{"unknown placeholder", notify.EventAccessRequestApproved, `{"title":"t","body":"{{machinename}}"}`},
		{"unknown event", "nope.event", `{"title":"t","body":"b"}`},
		{"empty title", notify.EventAccessRequestApproved, `{"title":"  ","body":"b"}`},
		{"empty body", notify.EventAccessRequestApproved, `{"title":"t","body":""}`},
		{"event mismatch", notify.EventAccessRequestApproved,
			`{"event_type":"access_request.rejected","title":"t","body":"b"}`},
		{"malformed json", notify.EventAccessRequestApproved, `{`},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			store := &templateStore{}
			s := &APIServer{store: store}

			c, rec := testContext(t, http.MethodPut, tc.body, gin.Params{{Key: "event", Value: tc.event}})
			s.putNotificationTemplate(c)

			if rec.Code != http.StatusBadRequest {
				t.Errorf("expected 400, got %d: %s", rec.Code, rec.Body.String())
			}
			if store.upserted != nil {
				t.Errorf("expected nothing stored, got %+v", store.upserted)
			}
		})
	}
}

// Copy is stored trimmed: surrounding whitespace is invisible in the editor but
// shows up in what gets delivered.
func TestPutNotificationTemplateNormalizesBeforeStoring(t *testing.T) {
	store := &templateStore{}
	s := &APIServer{store: store}

	c, rec := testContext(t, http.MethodPut,
		`{"title":"  Approved  ","body":"  Access to {{machine}}.  "}`,
		gin.Params{{Key: "event", Value: notify.EventAccessRequestApproved}})
	s.putNotificationTemplate(c)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	if store.upserted == nil {
		t.Fatal("expected the template to be stored")
	}
	if store.upserted.Title != "Approved" || store.upserted.Body != "Access to {{machine}}." {
		t.Errorf("expected trimmed copy, got %+v", store.upserted)
	}
	if store.upserted.EventType != notify.EventAccessRequestApproved {
		t.Errorf("expected the path event applied, got %q", store.upserted.EventType)
	}
}

// Reset drops the override and reports the built-in copy back, so the editor
// can repaint without a second request.
func TestDeleteNotificationTemplateRevertsToDefault(t *testing.T) {
	store := &templateStore{templates: []*notify.Template{{
		EventType: notify.EventAccessRequestApproved,
		Title:     "Custom",
		Body:      "Custom.",
	}}}
	s := &APIServer{store: store}

	c, rec := testContext(t, http.MethodDelete, "",
		gin.Params{{Key: "event", Value: notify.EventAccessRequestApproved}})
	s.deleteNotificationTemplate(c)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	if len(store.deletedEvents) != 1 || store.deletedEvents[0] != notify.EventAccessRequestApproved {
		t.Errorf("expected the override deleted, got %v", store.deletedEvents)
	}

	entry := decodeBody[struct {
		Title      string `json:"title"`
		Customized bool   `json:"customized"`
	}](t, rec)
	if entry.Title != "Access request approved" || entry.Customized {
		t.Errorf("expected the built-in copy reported as no longer customized, got %+v", entry)
	}

	// And the next delivery must use the default again.
	s.deliverNotification(context.Background(), "u1", notify.EventAccessRequestApproved,
		map[string]string{"machine": "lab-1"})
	if len(store.created) != 1 || store.created[0].Title != "Access request approved" {
		t.Errorf("expected the default delivered after reset, got %+v", store.created)
	}
}

func TestDeleteNotificationTemplateRejectsUnknownEvent(t *testing.T) {
	store := &templateStore{}
	s := &APIServer{store: store}

	c, rec := testContext(t, http.MethodDelete, "", gin.Params{{Key: "event", Value: "nope.event"}})
	s.deleteNotificationTemplate(c)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d: %s", rec.Code, rec.Body.String())
	}
	if len(store.deletedEvents) != 0 {
		t.Errorf("expected no delete attempted, got %v", store.deletedEvents)
	}
}
