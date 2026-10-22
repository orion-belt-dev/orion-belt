package chatops

import (
	"context"
	"crypto/ed25519"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/orion-belt-dev/orion-belt/pkg/plugin"
)

// recorded is one outbound request the plugin made.
type recorded struct {
	host   string // original host, before the transport redirected it
	method string
	path   string
	query  url.Values
	header http.Header
	body   string
}

// world is a single local server standing in for Slack, Discord, Teams,
// Rocket.Chat and the Orion Belt API. Requests to slack.com/discord.com are
// redirected to it by rewriteTransport, so nothing leaves the machine.
type world struct {
	t   *testing.T
	srv *httptest.Server

	mu       sync.Mutex
	requests []recorded
	got      chan recorded

	// Responses, keyed by "<host><path>" or a path prefix for the core API.
	slackBody     string
	discordStatus int
	teamsStatus   int
	rocketStatus  int
	resolveStatus int
	resolveBody   string
	usernames     map[string]string
	machines      map[string]string
}

func newWorld(t *testing.T) *world {
	t.Helper()
	prev := log.Writer()
	log.SetOutput(io.Discard)
	t.Cleanup(func() { log.SetOutput(prev) })

	w := &world{
		t:             t,
		got:           make(chan recorded, 64),
		slackBody:     `{"ok":true}`,
		discordStatus: http.StatusOK,
		teamsStatus:   http.StatusOK,
		rocketStatus:  http.StatusOK,
		resolveStatus: http.StatusOK,
		resolveBody:   `{}`,
		usernames:     map[string]string{"u-1": "alice"},
		machines:      map[string]string{"m-1": "web-01"},
	}
	w.srv = httptest.NewServer(http.HandlerFunc(w.serve))
	t.Cleanup(w.srv.Close)
	prevOrigin := slackResponseOrigin
	slackResponseOrigin = w.srv.URL
	t.Cleanup(func() { slackResponseOrigin = prevOrigin })
	return w
}

func (w *world) serve(rw http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	host := r.Header.Get("X-Original-Host")
	rec := recorded{host: host, method: r.Method, path: r.URL.Path, query: r.URL.Query(), header: r.Header.Clone(), body: string(body)}
	w.mu.Lock()
	w.requests = append(w.requests, rec)
	w.mu.Unlock()
	w.got <- rec

	switch {
	case host == "slack.com":
		_, _ = io.WriteString(rw, w.slackBody)
	case host == "discord.com":
		rw.WriteHeader(w.discordStatus)
		_, _ = io.WriteString(rw, `{"message":"discord says no"}`)
	case r.URL.Path == "/teams":
		rw.WriteHeader(w.teamsStatus)
	case r.URL.Path == "/rocket":
		rw.WriteHeader(w.rocketStatus)
	case r.URL.Path == "/slack-response":
		rw.WriteHeader(http.StatusOK)
	case strings.HasPrefix(r.URL.Path, "/api/v1/users/"):
		name, ok := w.usernames[strings.TrimPrefix(r.URL.Path, "/api/v1/users/")]
		if !ok {
			rw.WriteHeader(http.StatusNotFound)
			return
		}
		_ = json.NewEncoder(rw).Encode(map[string]string{"username": name})
	case strings.HasPrefix(r.URL.Path, "/api/v1/machines/"):
		name, ok := w.machines[strings.TrimPrefix(r.URL.Path, "/api/v1/machines/")]
		if !ok {
			rw.WriteHeader(http.StatusNotFound)
			return
		}
		_ = json.NewEncoder(rw).Encode(map[string]string{"name": name})
	case strings.HasPrefix(r.URL.Path, "/api/v1/admin/access-requests/"):
		rw.WriteHeader(w.resolveStatus)
		_, _ = io.WriteString(rw, w.resolveBody)
	default:
		w.t.Errorf("unexpected outbound request %s %s%s", r.Method, host, r.URL.Path)
		rw.WriteHeader(http.StatusTeapot)
	}
}

// byPath returns requests whose path, or original host plus path, starts with
// prefix.
func (w *world) byPath(prefix string) []recorded {
	w.mu.Lock()
	defer w.mu.Unlock()
	var out []recorded
	for _, r := range w.requests {
		if strings.HasPrefix(r.path, prefix) || strings.HasPrefix(r.host+r.path, prefix) {
			out = append(out, r)
		}
	}
	return out
}

// rewriteTransport sends every request to target, recording the original host.
type rewriteTransport struct{ target *url.URL }

func (rt rewriteTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	r = r.Clone(r.Context())
	r.Header.Set("X-Original-Host", r.URL.Host)
	r.URL.Scheme = rt.target.Scheme
	r.URL.Host = rt.target.Host
	r.Host = rt.target.Host
	return http.DefaultTransport.RoundTrip(r)
}

type failTransport struct{}

func (failTransport) RoundTrip(*http.Request) (*http.Response, error) {
	return nil, errors.New("network down")
}

type testKeys struct {
	slackSecret string
	discordPub  ed25519.PublicKey
	discordPriv ed25519.PrivateKey
	approval    string
}

// newConfiguredPlugin initialises the plugin with every platform enabled and
// all outbound traffic pointed at w.
func newConfiguredPlugin(t *testing.T, w *world) (*ChatOpsPlugin, testKeys) {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	keys := testKeys{slackSecret: "slack-signing", discordPub: pub, discordPriv: priv, approval: "approval-secret"}

	p := NewPlugin().(*ChatOpsPlugin)
	err = p.Initialize(context.Background(), map[string]interface{}{
		"api_base_url":    w.srv.URL + "/",
		"api_key":         "admin-key",
		"approval_secret": keys.approval,
		"public_base_url": "https://orion.example.com/hooks",
		"slack":           map[string]interface{}{"enabled": true, "bot_token": "xoxb-1", "signing_secret": keys.slackSecret, "channel": "C1"},
		"discord":         map[string]interface{}{"enabled": true, "bot_token": "d-1", "public_key": hex.EncodeToString(pub), "channel_id": "D1"},
		"teams":           map[string]interface{}{"enabled": true, "webhook_url": w.srv.URL + "/teams"},
		"rocketchat":      map[string]interface{}{"enabled": true, "webhook_url": w.srv.URL + "/rocket"},
	})
	if err != nil {
		t.Fatalf("Initialize: %v", err)
	}
	target, _ := url.Parse(w.srv.URL)
	p.httpClient = &http.Client{Transport: rewriteTransport{target: target}, Timeout: 5 * time.Second}
	return p, keys
}

func accessHook(data map[string]interface{}) *plugin.HookContext {
	return &plugin.HookContext{UserID: "u-1", MachineID: "m-1", Data: data}
}

// --- plugin surface --------------------------------------------------------------

func TestPluginIdentityAndSchema(t *testing.T) {
	p := NewPlugin().(*ChatOpsPlugin)
	if p.Name() != "chatops-access-request" || p.Version() != "1.0.0" {
		t.Errorf("identity = %s %s", p.Name(), p.Version())
	}
	if err := p.Shutdown(context.Background()); err != nil {
		t.Errorf("Shutdown = %v", err)
	}

	secrets := map[string]bool{}
	var walk func([]plugin.ConfigField)
	walk = func(fs []plugin.ConfigField) {
		for _, f := range fs {
			if f.Secret {
				secrets[f.Key] = true
			}
			walk(f.Fields)
		}
	}
	walk(p.ConfigSchema())
	for _, k := range []string{"api_key", "approval_secret", "bot_token", "signing_secret", "webhook_url"} {
		if !secrets[k] {
			t.Errorf("schema does not mark %s secret", k)
		}
	}
}

func TestInitialize_InvalidConfig(t *testing.T) {
	err := NewPlugin().Initialize(context.Background(), map[string]interface{}{})
	if err == nil || !strings.HasPrefix(err.Error(), "chatops-access-request: ") {
		t.Fatalf("Initialize = %v, want a prefixed config error", err)
	}
}

// --- OnHook ----------------------------------------------------------------------

func TestOnHook_PostsToEveryPlatformWithEnrichedText(t *testing.T) {
	w := newWorld(t)
	p, keys := newConfiguredPlugin(t, w)

	err := p.OnHook(context.Background(), plugin.HookAccessRequest, accessHook(map[string]interface{}{
		"request_id": "req-1", "reason": "hotfix", "duration": 1800,
	}))
	if err != nil {
		t.Fatalf("OnHook: %v", err)
	}

	slack := w.byPath("slack.com/api/chat.postMessage")
	if len(slack) != 1 || slack[0].header.Get("Authorization") != "Bearer xoxb-1" {
		t.Fatalf("slack requests = %+v", slack)
	}
	for _, want := range []string{"*alice*", "*web-01*", "Reason: hotfix", "Duration: 30m0s", `"action_id":"approve"`, `"value":"req-1"`} {
		if !strings.Contains(slack[0].body, want) {
			t.Errorf("slack body lacks %q: %s", want, slack[0].body)
		}
	}

	discord := w.byPath("discord.com/api/v10/channels/D1/messages")
	if len(discord) != 1 || discord[0].header.Get("Authorization") != "Bot d-1" || !strings.Contains(discord[0].body, `"custom_id":"deny:req-1"`) {
		t.Errorf("discord requests = %+v", discord)
	}

	for _, path := range []string{"/teams", "/rocket"} {
		reqs := w.byPath(path)
		if len(reqs) != 1 {
			t.Fatalf("%s requests = %d, want 1", path, len(reqs))
		}
		link := extractLink(t, reqs[0].body, "/approve?token=")
		claims, err := verifyMagicLink(keys.approval, link)
		if err != nil || claims.RequestID != "req-1" || claims.Action != "approve" {
			t.Errorf("%s approve link claims = %+v, %v", path, claims, err)
		}
	}

	for _, r := range w.byPath("/api/v1/") {
		if r.header.Get("X-API-Key") != "admin-key" {
			t.Errorf("core API call %s without the admin key", r.path)
		}
	}
}

// extractLink returns the token following marker in a JSON body.
func extractLink(t *testing.T, body, marker string) string {
	t.Helper()
	i := strings.Index(body, marker)
	if i < 0 {
		t.Fatalf("body lacks %q: %s", marker, body)
	}
	rest := body[i+len(marker):]
	return rest[:strings.IndexByte(rest, '"')]
}

func TestOnHook_IgnoresOtherHooksAndValidatesInput(t *testing.T) {
	w := newWorld(t)
	p := NewPlugin().(*ChatOpsPlugin)

	if err := p.OnHook(context.Background(), plugin.HookSessionStart, accessHook(nil)); err != nil {
		t.Errorf("non-access hook = %v, want nil", err)
	}
	if err := p.OnHook(context.Background(), plugin.HookAccessRequest, accessHook(nil)); err == nil || !strings.Contains(err.Error(), "not configured") {
		t.Errorf("unconfigured = %v", err)
	}

	p, _ = newConfiguredPlugin(t, w)
	if err := p.OnHook(context.Background(), plugin.HookAccessRequest, accessHook(map[string]interface{}{})); err == nil || !strings.Contains(err.Error(), "missing request_id") {
		t.Errorf("no request id = %v", err)
	}

	p.cfg = &Config{}
	p.api = newAPIClient(w.srv.URL, "k")
	if err := p.OnHook(context.Background(), plugin.HookAccessRequest, accessHook(map[string]interface{}{"request_id": "r"})); err == nil || !strings.Contains(err.Error(), "no platform") {
		t.Errorf("nothing enabled = %v", err)
	}
}

func TestOnHook_PartialAndTotalFailure(t *testing.T) {
	w := newWorld(t)
	p, _ := newConfiguredPlugin(t, w)
	w.slackBody = `{"ok":false,"error":"channel_not_found"}`
	w.discordStatus = http.StatusForbidden
	w.teamsStatus = http.StatusBadRequest

	hook := accessHook(map[string]interface{}{"request_id": "r"})
	if err := p.OnHook(context.Background(), plugin.HookAccessRequest, hook); err != nil {
		t.Fatalf("one platform still works, OnHook = %v, want nil", err)
	}

	w.rocketStatus = http.StatusInternalServerError
	err := p.OnHook(context.Background(), plugin.HookAccessRequest, hook)
	if err == nil || !strings.Contains(err.Error(), "all 4 configured platform(s) failed") {
		t.Errorf("all failing = %v", err)
	}
}

func TestBuildMessage_FallsBackToIDs(t *testing.T) {
	w := newWorld(t)
	p, _ := newConfiguredPlugin(t, w)

	msg := p.buildMessage(context.Background(), &plugin.HookContext{UserID: "u-unknown", MachineID: "m-unknown",
		Data: map[string]interface{}{"duration": float64(60)}}, "r")
	if !strings.Contains(msg, "*u-unknown*") || !strings.Contains(msg, "*m-unknown*") || !strings.Contains(msg, "Duration: 1m0s") {
		t.Errorf("message = %q, want raw IDs and the float duration", msg)
	}
	if strings.Contains(msg, "Reason:") {
		t.Errorf("message = %q, want no reason line", msg)
	}
}

// --- outbound posts ----------------------------------------------------------------

func TestPostSlack_Errors(t *testing.T) {
	w := newWorld(t)
	p, _ := newConfiguredPlugin(t, w)
	ctx := context.Background()

	w.slackBody = "not json"
	if err := p.postSlack(ctx, "t", "r"); err == nil || !strings.Contains(err.Error(), "decode slack response") {
		t.Errorf("non-JSON = %v", err)
	}
	w.slackBody = `{"ok":false,"error":"invalid_auth"}`
	if err := p.postSlack(ctx, "t", "r"); err == nil || !strings.Contains(err.Error(), "invalid_auth") {
		t.Errorf("ok=false = %v", err)
	}

	p.httpClient = &http.Client{Transport: failTransport{}}
	for name, post := range map[string]func() error{
		"slack":      func() error { return p.postSlack(ctx, "t", "r") },
		"discord":    func() error { return p.postDiscord(ctx, "t", "r") },
		"teams":      func() error { return p.postTeams(ctx, "t", "r") },
		"rocketchat": func() error { return p.postRocketChat(ctx, "t", "r") },
	} {
		if err := post(); err == nil || !strings.Contains(err.Error(), "network down") {
			t.Errorf("%s with network down = %v", name, err)
		}
	}
}

func TestPostWebhooks_InvalidURL(t *testing.T) {
	w := newWorld(t)
	p, _ := newConfiguredPlugin(t, w)
	p.cfg.Teams.WebhookURL = "://bad"
	p.cfg.RocketChat.WebhookURL = "://bad"
	p.cfg.Discord.ChannelID = "bad\x7f"
	ctx := context.Background()

	if err := p.postTeams(ctx, "t", "r"); err == nil || !strings.Contains(err.Error(), "build teams request") {
		t.Errorf("teams = %v", err)
	}
	if err := p.postRocketChat(ctx, "t", "r"); err == nil || !strings.Contains(err.Error(), "build rocketchat request") {
		t.Errorf("rocketchat = %v", err)
	}
	if err := p.postDiscord(ctx, "t", "r"); err == nil || !strings.Contains(err.Error(), "build discord request") {
		t.Errorf("discord = %v", err)
	}
}

func TestPostSlackResponseURL(t *testing.T) {
	w := newWorld(t)
	p, _ := newConfiguredPlugin(t, w)

	p.postSlackResponseURL(w.srv.URL+"/slack-response?t=abc", "Approved by alice")
	reqs := w.byPath("/slack-response")
	if len(reqs) != 1 || !strings.Contains(reqs[0].body, `"replace_original":true`) || !strings.Contains(reqs[0].body, "Approved by alice") {
		t.Errorf("response_url requests = %+v", reqs)
	}
	if len(reqs) == 1 && reqs[0].query.Get("t") != "abc" {
		t.Errorf("response_url query = %v, want t=abc", reqs[0].query)
	}

	p.postSlackResponseURL("://bad", "x") // invalid URL: returns quietly
	// Anything but Slack's origin is refused before a request is made.
	var hits atomic.Int32
	other := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { hits.Add(1) }))
	defer other.Close()
	p.postSlackResponseURL(other.URL+"/slack-response", "x")
	if n := hits.Load(); n != 0 {
		t.Errorf("response_url outside the Slack origin was called %d times", n)
	}
	p.httpClient = &http.Client{Transport: failTransport{}}
	p.postSlackResponseURL(w.srv.URL+"/slack-response", "x") // network error: logged only
	if n := len(w.byPath("/slack-response")); n != 1 {
		t.Errorf("response_url requests = %d after failures, want still 1", n)
	}
}

// --- HTTP handler ------------------------------------------------------------------

func serve(t *testing.T, h http.Handler, method, target string, body string, header http.Header) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, target, strings.NewReader(body))
	for k, v := range header {
		req.Header[k] = v
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func TestHandler_MethodAndConfigurationGuards(t *testing.T) {
	unconfigured := NewPlugin().(*ChatOpsPlugin).Handler()
	cases := []struct {
		method, path string
		want         int
	}{
		{http.MethodGet, "/slack/interactions", http.StatusMethodNotAllowed},
		{http.MethodGet, "/discord/interactions", http.StatusMethodNotAllowed},
		{http.MethodPost, "/approve", http.StatusMethodNotAllowed},
		{http.MethodPost, "/deny", http.StatusMethodNotAllowed},
		{http.MethodPost, "/slack/interactions", http.StatusNotFound},
		{http.MethodPost, "/discord/interactions", http.StatusNotFound},
		{http.MethodGet, "/approve", http.StatusNotFound},
		{http.MethodGet, "/deny", http.StatusNotFound},
	}
	for _, tc := range cases {
		if rec := serve(t, unconfigured, tc.method, tc.path, "", nil); rec.Code != tc.want {
			t.Errorf("%s %s = %d, want %d", tc.method, tc.path, rec.Code, tc.want)
		}
	}
}

func slackHeaders(secret, body string, ts time.Time) http.Header {
	stamp := strconv.FormatInt(ts.Unix(), 10)
	return http.Header{
		"X-Slack-Signature":         {makeSlackSignature(secret, stamp, body)},
		"X-Slack-Request-Timestamp": {stamp},
	}
}

func slackForm(payload string) string {
	return url.Values{"payload": {payload}}.Encode()
}

func TestSlackInteraction(t *testing.T) {
	w := newWorld(t)
	p, keys := newConfiguredPlugin(t, w)
	h := p.Handler()

	cases := []struct {
		name string
		body string
		sign bool
		want int
	}{
		{"bad signature", slackForm(`{}`), false, http.StatusUnauthorized},
		{"bad form", "%zz", true, http.StatusBadRequest},
		{"missing payload", "a=b", true, http.StatusBadRequest},
		{"invalid json", slackForm("{"), true, http.StatusBadRequest},
		{"no actions", slackForm(`{"actions":[]}`), true, http.StatusBadRequest},
		{"unknown action", slackForm(`{"actions":[{"action_id":"maybe","value":"r"}]}`), true, http.StatusBadRequest},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			hdr := http.Header{"X-Slack-Signature": {"v0=00"}, "X-Slack-Request-Timestamp": {strconv.FormatInt(time.Now().Unix(), 10)}}
			if tc.sign {
				hdr = slackHeaders(keys.slackSecret, tc.body, time.Now())
			}
			if rec := serve(t, h, http.MethodPost, "/slack/interactions", tc.body, hdr); rec.Code != tc.want {
				t.Errorf("status = %d, want %d (%s)", rec.Code, tc.want, rec.Body.String())
			}
		})
	}
}

func TestSlackInteraction_ResolvesAndUpdatesMessage(t *testing.T) {
	cases := []struct {
		name, action, user, wantRoute, wantText string
		resolveStatus                           int
	}{
		{"approve by username", "approve", `{"username":"bob"}`, "/approve", "Approved by bob", http.StatusOK},
		{"deny falls back to name", "deny", `{"name":"Bob B"}`, "/reject", "Denied by Bob B", http.StatusOK},
		{"api failure is reported", "approve", `{"username":"bob"}`, "/approve", "Failed to approve request `req-9`: request is not pending", http.StatusConflict},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			w := newWorld(t)
			w.resolveStatus = tc.resolveStatus
			if tc.resolveStatus != http.StatusOK {
				w.resolveBody = `{"error":"request is not pending"}`
			}
			p, keys := newConfiguredPlugin(t, w)
			payload := `{"actions":[{"action_id":"` + tc.action + `","value":"req-9"}],"response_url":"` + w.srv.URL + `/slack-response","user":` + tc.user + `}`
			body := slackForm(payload)

			rec := serve(t, p.Handler(), http.MethodPost, "/slack/interactions", body, slackHeaders(keys.slackSecret, body, time.Now()))
			if rec.Code != http.StatusOK {
				t.Fatalf("status = %d (%s)", rec.Code, rec.Body.String())
			}
			if got := w.byPath("/api/v1/admin/access-requests/req-9" + tc.wantRoute); len(got) != 1 {
				t.Errorf("resolve calls on %s = %d, want 1", tc.wantRoute, len(got))
			}
			if update := waitFor(t, w, "/slack-response"); !strings.Contains(update.body, tc.wantText) {
				t.Errorf("message update = %s, want %q", update.body, tc.wantText)
			}
		})
	}
}

// waitFor blocks until the world records a request to path. The Slack
// message update runs on its own goroutine, so it is not done when the
// handler returns.
func waitFor(t *testing.T, w *world, path string) recorded {
	t.Helper()
	deadline := time.After(5 * time.Second)
	for {
		select {
		case r := <-w.got:
			if r.path == path {
				return r
			}
		case <-deadline:
			t.Fatalf("no request to %s", path)
		}
	}
}

func signDiscord(keys testKeys, body string) http.Header {
	ts := "1700000000"
	sig := ed25519.Sign(keys.discordPriv, append([]byte(ts), body...))
	return http.Header{"X-Signature-Ed25519": {hex.EncodeToString(sig)}, "X-Signature-Timestamp": {ts}}
}

func TestDiscordInteraction(t *testing.T) {
	w := newWorld(t)
	p, keys := newConfiguredPlugin(t, w)
	h := p.Handler()

	unsigned := serve(t, h, http.MethodPost, "/discord/interactions", `{"type":1}`, http.Header{"X-Signature-Ed25519": {"00"}})
	if unsigned.Code != http.StatusUnauthorized {
		t.Errorf("unsigned = %d, want 401", unsigned.Code)
	}

	cases := []struct {
		name     string
		body     string
		wantCode int
		wantBody string
	}{
		{"invalid json", "{", http.StatusBadRequest, "invalid interaction JSON"},
		{"ping", `{"type":1}`, http.StatusOK, `{"type":1}`},
		{"unknown type", `{"type":2}`, http.StatusOK, ""},
		{"malformed custom id", `{"type":3,"data":{"custom_id":"approve"}}`, http.StatusBadRequest, "malformed custom_id"},
		{"unknown action", `{"type":3,"data":{"custom_id":"maybe:r"}}`, http.StatusBadRequest, "unknown action"},
		{"approve", `{"type":3,"data":{"custom_id":"approve:r-1"}}`, http.StatusOK, "Approved via Discord"},
		{"deny", `{"type":3,"data":{"custom_id":"deny:r-1"}}`, http.StatusOK, "Denied via Discord"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := serve(t, h, http.MethodPost, "/discord/interactions", tc.body, signDiscord(keys, tc.body))
			if rec.Code != tc.wantCode || !strings.Contains(rec.Body.String(), tc.wantBody) {
				t.Errorf("= %d %q, want %d containing %q", rec.Code, rec.Body.String(), tc.wantCode, tc.wantBody)
			}
		})
	}

	w.resolveStatus = http.StatusInternalServerError
	w.resolveBody = "oops"
	body := `{"type":3,"data":{"custom_id":"deny:r-2"}}`
	rec := serve(t, h, http.MethodPost, "/discord/interactions", body, signDiscord(keys, body))
	if !strings.Contains(rec.Body.String(), "Failed to deny request r-2: request failed with status 500") {
		t.Errorf("failure update = %s", rec.Body.String())
	}
}

func TestMagicLinks(t *testing.T) {
	w := newWorld(t)
	p, keys := newConfiguredPlugin(t, w)
	h := p.Handler()

	token := func(action string) string {
		link, err := signMagicLink("", keys.approval, "req-7", action)
		if err != nil {
			t.Fatal(err)
		}
		return strings.SplitN(link, "token=", 2)[1]
	}

	cases := []struct {
		name, path string
		wantCode   int
		wantText   string
	}{
		{"missing token", "/approve", http.StatusBadRequest, "Missing token."},
		{"tampered token", "/approve?token=abc.00", http.StatusBadRequest, "Invalid or expired link"},
		{"action mismatch", "/deny?token=" + token("approve"), http.StatusBadRequest, "does not match"},
		{"approve", "/approve?token=" + token("approve"), http.StatusOK, "Access request approved."},
		{"deny", "/deny?token=" + token("deny"), http.StatusOK, "Access request denied."},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := serve(t, h, http.MethodGet, tc.path, "", nil)
			if rec.Code != tc.wantCode || !strings.Contains(rec.Body.String(), tc.wantText) {
				t.Errorf("= %d, body lacks %q:\n%s", rec.Code, tc.wantText, rec.Body.String())
			}
			if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/html") {
				t.Errorf("Content-Type = %q, want HTML", ct)
			}
		})
	}

	w.resolveStatus = http.StatusNotFound
	w.resolveBody = `{"error":"request not found"}`
	rec := serve(t, h, http.MethodGet, "/approve?token="+token("approve"), "", nil)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "Failed to approve request: request not found") {
		t.Errorf("failed resolve = %d %s", rec.Code, rec.Body.String())
	}
}

// --- core API client -------------------------------------------------------------

func TestAPIClient_Errors(t *testing.T) {
	w := newWorld(t)
	ctx := context.Background()

	bad := newAPIClient("http://bad\x7fhost", "k")
	if _, err := bad.GetUsername(ctx, "u"); err == nil || !strings.Contains(err.Error(), "build request") {
		t.Errorf("bad base URL = %v", err)
	}
	if _, err := bad.GetMachineName(ctx, "m"); err == nil {
		t.Error("GetMachineName with bad base URL succeeded")
	}
	if err := bad.resolveAction(ctx, "r", "approve"); err == nil {
		t.Error("resolveAction with bad base URL succeeded")
	}

	down := newAPIClient("http://127.0.0.1:1", "k")
	if _, err := down.GetUsername(ctx, "u"); err == nil || !strings.Contains(err.Error(), "request failed") {
		t.Errorf("server down = %v", err)
	}

	c := newAPIClient(w.srv.URL, "k")
	if err := c.resolveAction(ctx, "r", "escalate"); err == nil || !strings.Contains(err.Error(), "unknown action") {
		t.Errorf("unknown action = %v", err)
	}
	if _, err := c.GetMachineName(ctx, "absent"); err == nil || !strings.Contains(err.Error(), "status 404") {
		t.Errorf("missing machine = %v", err)
	}

	w.usernames["blank"] = ""
	w.machines["blank"] = ""
	if _, err := c.GetUsername(ctx, "blank"); err == nil || !strings.Contains(err.Error(), "no username") {
		t.Errorf("blank username = %v", err)
	}
	if _, err := c.GetMachineName(ctx, "blank"); err == nil || !strings.Contains(err.Error(), "no name") {
		t.Errorf("blank machine name = %v", err)
	}
}

func TestAPIClient_UndecodableResponses(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, "not json")
	}))
	defer srv.Close()
	c := newAPIClient(srv.URL, "k")
	if _, err := c.GetUsername(context.Background(), "u"); err == nil || !strings.Contains(err.Error(), "decode user") {
		t.Errorf("GetUsername = %v", err)
	}
	if _, err := c.GetMachineName(context.Background(), "m"); err == nil || !strings.Contains(err.Error(), "decode machine") {
		t.Errorf("GetMachineName = %v", err)
	}
}

func TestAPIClient_TruncatedBody(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Length", "100")
		_, _ = io.WriteString(w, "short")
	}))
	defer srv.Close()
	if _, err := newAPIClient(srv.URL, "k").GetUsername(context.Background(), "u"); err == nil || !strings.Contains(err.Error(), "read response") {
		t.Errorf("truncated body = %v", err)
	}
}

// --- config and signing edge cases ------------------------------------------------

func TestParseConfig_RequiredFields(t *testing.T) {
	base := func() map[string]interface{} {
		return map[string]interface{}{"api_base_url": "http://x", "api_key": "k"}
	}
	cases := []struct {
		name  string
		extra map[string]interface{}
		want  string
	}{
		{"slack block not an object", map[string]interface{}{"slack": "yes"}, "slack must be an object"},
		{"slack wrong type", map[string]interface{}{"slack": map[string]interface{}{"enabled": "yes"}}, "unmarshal slack config"},
		{"slack unmarshalable", map[string]interface{}{"slack": map[string]interface{}{"x": make(chan int)}}, "marshal slack config"},
		{"discord not object", map[string]interface{}{"discord": 1}, "discord must be an object"},
		{"teams not object", map[string]interface{}{"teams": 1}, "teams must be an object"},
		{"rocketchat not object", map[string]interface{}{"rocketchat": 1}, "rocketchat must be an object"},
		{"slack token", map[string]interface{}{"slack": map[string]interface{}{"enabled": true}}, "slack.bot_token"},
		{"slack signing secret", map[string]interface{}{"slack": map[string]interface{}{"enabled": true, "bot_token": "t"}}, "slack.signing_secret"},
		{"slack channel", map[string]interface{}{"slack": map[string]interface{}{"enabled": true, "bot_token": "t", "signing_secret": "s"}}, "slack.channel"},
		{"discord token", map[string]interface{}{"discord": map[string]interface{}{"enabled": true}}, "discord.bot_token"},
		{"discord public key", map[string]interface{}{"discord": map[string]interface{}{"enabled": true, "bot_token": "t"}}, "discord.public_key"},
		{"discord channel", map[string]interface{}{"discord": map[string]interface{}{"enabled": true, "bot_token": "t", "public_key": "p"}}, "discord.channel_id"},
		{"teams url", map[string]interface{}{"teams": map[string]interface{}{"enabled": true}}, "teams.webhook_url"},
		{"rocketchat url", map[string]interface{}{"rocketchat": map[string]interface{}{"enabled": true}}, "rocketchat.webhook_url"},
		{"public base url", map[string]interface{}{"approval_secret": "s", "rocketchat": map[string]interface{}{"enabled": true, "webhook_url": "u"}}, "public_base_url"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			raw := base()
			for k, v := range tc.extra {
				raw[k] = v
			}
			if _, err := parseConfig(raw); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Errorf("parseConfig = %v, want %q", err, tc.want)
			}
		})
	}
}

func TestSignMagicLink_UnknownAction(t *testing.T) {
	if _, err := signMagicLink("https://x", "s", "r", "escalate"); err == nil {
		t.Error("signMagicLink accepted an unknown action")
	}
}

func TestVerifyMagicLink_MalformedParts(t *testing.T) {
	secret := "s"
	signed := func(encoded string) string {
		return encoded + "." + hmacHex(secret, encoded)
	}
	cases := map[string]string{
		"non-hex signature": "abc.zz",
		"bad base64":        signed("!!!"),
		"bad json":          signed("bm90IGpzb24"), // "not json"
		"invalid claims":    signed("eyJyZXF1ZXN0X2lkIjoiIiwiYWN0aW9uIjoiYXBwcm92ZSJ9"),
	}
	for name, token := range cases {
		if _, err := verifyMagicLink(secret, token); err == nil {
			t.Errorf("%s: verifyMagicLink accepted %q", name, token)
		}
	}
}

func TestVerifySlackSignature_MoreFailures(t *testing.T) {
	now := strconv.FormatInt(time.Now().Unix(), 10)
	future := strconv.FormatInt(time.Now().Add(10*time.Minute).Unix(), 10)
	cases := map[string][2]string{
		"bad timestamp":  {"v0=00", "yesterday"},
		"future skew":    {"v0=00", future},
		"missing prefix": {"00", now},
		"non-hex":        {"v0=zz", now},
	}
	for name, hdr := range cases {
		if err := verifySlackSignature("s", hdr[0], hdr[1], nil); err == nil {
			t.Errorf("%s: verifySlackSignature accepted it", name)
		}
	}
}

func hmacHex(secret, data string) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(data))
	return hex.EncodeToString(mac.Sum(nil))
}
