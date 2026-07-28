package authz

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/zrougamed/orion-belt/pkg/common"
)

// capture records what the fake OpenFGA server received so tests can assert on
// the wire format — the tuple shape is the contract with OpenFGA, and a typo in
// a key name fails open (or closed) at runtime rather than at compile time.
type capture struct {
	path   string
	auth   string
	ctype  string
	body   map[string]interface{}
	called int
}

func newFakeFGA(t *testing.T, status int, response string) (*httptest.Server, *capture) {
	t.Helper()
	cap := &capture{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cap.called++
		cap.path = r.URL.Path
		cap.auth = r.Header.Get("Authorization")
		cap.ctype = r.Header.Get("Content-Type")

		raw, _ := io.ReadAll(r.Body)
		if len(raw) > 0 {
			if err := json.Unmarshal(raw, &cap.body); err != nil {
				t.Errorf("server received non-JSON body %q: %v", raw, err)
			}
		}
		if r.Method != http.MethodPost {
			t.Errorf("method = %s, want POST", r.Method)
		}
		w.WriteHeader(status)
		io.WriteString(w, response)
	}))
	t.Cleanup(srv.Close)
	return srv, cap
}

func enabledConfig(url string) common.OpenFGAConfig {
	return common.OpenFGAConfig{
		Enabled: true,
		APIURL:  url,
		StoreID: "store-1",
	}
}

func mustClient(t *testing.T, cfg common.OpenFGAConfig) *OpenFGA {
	t.Helper()
	o, err := NewOpenFGA(cfg, nil)
	if err != nil {
		t.Fatalf("NewOpenFGA: %v", err)
	}
	if o == nil {
		t.Fatal("NewOpenFGA returned a nil client for an enabled config")
	}
	return o
}

// tupleKey digs the tuple out of a Check request body.
func tupleKey(t *testing.T, body map[string]interface{}) map[string]interface{} {
	t.Helper()
	tk, ok := body["tuple_key"].(map[string]interface{})
	if !ok {
		t.Fatalf("body has no tuple_key object: %#v", body)
	}
	return tk
}

// firstWriteTuple digs the single tuple out of a write/delete request body.
func firstWriteTuple(t *testing.T, body map[string]interface{}, section string) map[string]interface{} {
	t.Helper()
	sec, ok := body[section].(map[string]interface{})
	if !ok {
		t.Fatalf("body has no %q object: %#v", section, body)
	}
	keys, ok := sec["tuple_keys"].([]interface{})
	if !ok || len(keys) != 1 {
		t.Fatalf("%s.tuple_keys is not a 1-element array: %#v", section, sec)
	}
	tuple, ok := keys[0].(map[string]interface{})
	if !ok {
		t.Fatalf("%s.tuple_keys[0] is not an object: %#v", section, keys[0])
	}
	return tuple
}

// --- construction -----------------------------------------------------------

// A disabled config yields (nil, nil) rather than an error: callers store the
// result and treat a nil Authorizer as "authorization not in use".
func TestNewOpenFGADisabledReturnsNilClientAndNoError(t *testing.T) {
	o, err := NewOpenFGA(common.OpenFGAConfig{Enabled: false}, nil)
	if err != nil {
		t.Fatalf("NewOpenFGA(disabled) returned error %v, want nil", err)
	}
	if o != nil {
		t.Error("NewOpenFGA(disabled) returned a client, want nil")
	}
}

// Disabled wins over incomplete settings — a half-filled but switched-off block
// must not break startup.
func TestNewOpenFGADisabledIgnoresMissingFields(t *testing.T) {
	o, err := NewOpenFGA(common.OpenFGAConfig{Enabled: false, APIURL: "", StoreID: ""}, nil)
	if err != nil || o != nil {
		t.Errorf("NewOpenFGA(disabled, empty) = (%v, %v), want (nil, nil)", o, err)
	}
}

func TestNewOpenFGAEnabledRequiresAPIURLAndStoreID(t *testing.T) {
	cases := map[string]common.OpenFGAConfig{
		"missing api_url":  {Enabled: true, StoreID: "store-1"},
		"missing store_id": {Enabled: true, APIURL: "http://fga.local"},
		"missing both":     {Enabled: true},
	}
	for name, cfg := range cases {
		t.Run(name, func(t *testing.T) {
			o, err := NewOpenFGA(cfg, nil)
			if err == nil {
				t.Fatal("NewOpenFGA succeeded, want a configuration error")
			}
			if o != nil {
				t.Error("NewOpenFGA returned a client alongside an error, want nil")
			}
		})
	}
}

func TestNewOpenFGADefaultsRelationToCanAccess(t *testing.T) {
	o := mustClient(t, enabledConfig("http://fga.local"))
	if o.relation != "can_access" {
		t.Errorf("relation = %q, want the %q default", o.relation, "can_access")
	}
}

func TestNewOpenFGAHonoursExplicitRelation(t *testing.T) {
	cfg := enabledConfig("http://fga.local")
	cfg.Relation = "viewer"

	if o := mustClient(t, cfg); o.relation != "viewer" {
		t.Errorf("relation = %q, want %q", o.relation, "viewer")
	}
}

// The API URL is concatenated with paths that already begin with "/", so a
// configured trailing slash would produce "//stores/...".
func TestNewOpenFGATrimsTrailingSlashFromAPIURL(t *testing.T) {
	o := mustClient(t, enabledConfig("http://fga.local/"))
	if o.apiURL != "http://fga.local" {
		t.Errorf("apiURL = %q, want the trailing slash trimmed", o.apiURL)
	}
}

// --- Check ------------------------------------------------------------------

func TestCheckReturnsAllowedDecision(t *testing.T) {
	for _, allowed := range []bool{true, false} {
		t.Run(map[bool]string{true: "allowed", false: "denied"}[allowed], func(t *testing.T) {
			body := `{"allowed":false}`
			if allowed {
				body = `{"allowed":true}`
			}
			srv, _ := newFakeFGA(t, http.StatusOK, body)

			got, err := mustClient(t, enabledConfig(srv.URL)).Check(context.Background(), "u1", "m1", "ssh")
			if err != nil {
				t.Fatalf("Check: %v", err)
			}
			if got != allowed {
				t.Errorf("Check = %v, want %v", got, allowed)
			}
		})
	}
}

// An OpenFGA response that omits "allowed" must deny, not default to true.
func TestCheckDeniesWhenResponseOmitsAllowed(t *testing.T) {
	srv, _ := newFakeFGA(t, http.StatusOK, `{}`)

	got, err := mustClient(t, enabledConfig(srv.URL)).Check(context.Background(), "u1", "m1", "ssh")
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if got {
		t.Error("Check = true for a response without an \"allowed\" field, want false")
	}
}

func TestCheckSendsCorrectPathAndTuple(t *testing.T) {
	srv, cap := newFakeFGA(t, http.StatusOK, `{"allowed":true}`)

	cfg := enabledConfig(srv.URL)
	cfg.StoreID = "store-xyz"
	if _, err := mustClient(t, cfg).Check(context.Background(), "alice", "web-01", "ssh"); err != nil {
		t.Fatalf("Check: %v", err)
	}

	if want := "/stores/store-xyz/check"; cap.path != want {
		t.Errorf("path = %q, want %q", cap.path, want)
	}
	if cap.ctype != "application/json" {
		t.Errorf("Content-Type = %q, want application/json", cap.ctype)
	}

	tk := tupleKey(t, cap.body)
	for key, want := range map[string]string{
		"user":     "user:alice",
		"relation": "can_access",
		"object":   "machine:web-01",
	} {
		if got := tk[key]; got != want {
			t.Errorf("tuple_key[%q] = %v, want %q", key, got, want)
		}
	}
}

func TestCheckForwardsAccessTypeAsContext(t *testing.T) {
	srv, cap := newFakeFGA(t, http.StatusOK, `{"allowed":true}`)

	if _, err := mustClient(t, enabledConfig(srv.URL)).Check(context.Background(), "u1", "m1", "sftp"); err != nil {
		t.Fatalf("Check: %v", err)
	}

	ctx, ok := cap.body["context"].(map[string]interface{})
	if !ok {
		t.Fatalf("body has no context object: %#v", cap.body)
	}
	if got := ctx["access_type"]; got != "sftp" {
		t.Errorf("context.access_type = %v, want %q", got, "sftp")
	}
}

// --- authorization model id --------------------------------------------------

func TestModelIDIncludedOnlyWhenConfigured(t *testing.T) {
	t.Run("omitted when empty", func(t *testing.T) {
		srv, cap := newFakeFGA(t, http.StatusOK, `{"allowed":true}`)

		if _, err := mustClient(t, enabledConfig(srv.URL)).Check(context.Background(), "u", "m", "ssh"); err != nil {
			t.Fatalf("Check: %v", err)
		}
		if _, present := cap.body["authorization_model_id"]; present {
			t.Error("authorization_model_id was sent despite no model id being configured")
		}
	})

	t.Run("sent when configured", func(t *testing.T) {
		srv, cap := newFakeFGA(t, http.StatusOK, `{"allowed":true}`)
		cfg := enabledConfig(srv.URL)
		cfg.ModelID = "model-42"

		if _, err := mustClient(t, cfg).Check(context.Background(), "u", "m", "ssh"); err != nil {
			t.Fatalf("Check: %v", err)
		}
		if got := cap.body["authorization_model_id"]; got != "model-42" {
			t.Errorf("authorization_model_id = %v, want %q", got, "model-42")
		}
	})
}

// --- WriteGrant / DeleteGrant ------------------------------------------------

// Both grant operations hit the same /write endpoint and are distinguished
// only by the "writes" vs "deletes" section — swapping them would silently
// invert the effect of an access grant.
func TestWriteGrantSendsWritesSection(t *testing.T) {
	srv, cap := newFakeFGA(t, http.StatusOK, `{}`)

	if err := mustClient(t, enabledConfig(srv.URL)).WriteGrant(context.Background(), "bob", "db-02", "ssh"); err != nil {
		t.Fatalf("WriteGrant: %v", err)
	}

	if want := "/stores/store-1/write"; cap.path != want {
		t.Errorf("path = %q, want %q", cap.path, want)
	}
	if _, present := cap.body["deletes"]; present {
		t.Error("WriteGrant sent a \"deletes\" section")
	}

	tuple := firstWriteTuple(t, cap.body, "writes")
	if tuple["user"] != "user:bob" || tuple["object"] != "machine:db-02" || tuple["relation"] != "can_access" {
		t.Errorf("write tuple = %#v, want user:bob / can_access / machine:db-02", tuple)
	}
}

func TestDeleteGrantSendsDeletesSection(t *testing.T) {
	srv, cap := newFakeFGA(t, http.StatusOK, `{}`)

	if err := mustClient(t, enabledConfig(srv.URL)).DeleteGrant(context.Background(), "bob", "db-02", "ssh"); err != nil {
		t.Fatalf("DeleteGrant: %v", err)
	}

	if want := "/stores/store-1/write"; cap.path != want {
		t.Errorf("path = %q, want %q", cap.path, want)
	}
	if _, present := cap.body["writes"]; present {
		t.Error("DeleteGrant sent a \"writes\" section")
	}

	tuple := firstWriteTuple(t, cap.body, "deletes")
	if tuple["user"] != "user:bob" || tuple["object"] != "machine:db-02" {
		t.Errorf("delete tuple = %#v, want user:bob / machine:db-02", tuple)
	}
}

func TestGrantsUseConfiguredRelation(t *testing.T) {
	srv, cap := newFakeFGA(t, http.StatusOK, `{}`)
	cfg := enabledConfig(srv.URL)
	cfg.Relation = "viewer"

	if err := mustClient(t, cfg).WriteGrant(context.Background(), "u", "m", "ssh"); err != nil {
		t.Fatalf("WriteGrant: %v", err)
	}
	if got := firstWriteTuple(t, cap.body, "writes")["relation"]; got != "viewer" {
		t.Errorf("relation = %v, want %q", got, "viewer")
	}
}

func TestModelIDIncludedOnGrants(t *testing.T) {
	cfg := func(url string) common.OpenFGAConfig {
		c := enabledConfig(url)
		c.ModelID = "model-7"
		return c
	}

	t.Run("write", func(t *testing.T) {
		srv, cap := newFakeFGA(t, http.StatusOK, `{}`)
		if err := mustClient(t, cfg(srv.URL)).WriteGrant(context.Background(), "u", "m", "ssh"); err != nil {
			t.Fatalf("WriteGrant: %v", err)
		}
		if got := cap.body["authorization_model_id"]; got != "model-7" {
			t.Errorf("authorization_model_id = %v, want %q", got, "model-7")
		}
	})

	t.Run("delete", func(t *testing.T) {
		srv, cap := newFakeFGA(t, http.StatusOK, `{}`)
		if err := mustClient(t, cfg(srv.URL)).DeleteGrant(context.Background(), "u", "m", "ssh"); err != nil {
			t.Fatalf("DeleteGrant: %v", err)
		}
		if got := cap.body["authorization_model_id"]; got != "model-7" {
			t.Errorf("authorization_model_id = %v, want %q", got, "model-7")
		}
	})
}

// --- auth header -------------------------------------------------------------

func TestAPITokenSentAsBearerWhenConfigured(t *testing.T) {
	srv, cap := newFakeFGA(t, http.StatusOK, `{"allowed":true}`)
	cfg := enabledConfig(srv.URL)
	cfg.APIToken = "s3cret"

	if _, err := mustClient(t, cfg).Check(context.Background(), "u", "m", "ssh"); err != nil {
		t.Fatalf("Check: %v", err)
	}
	if want := "Bearer s3cret"; cap.auth != want {
		t.Errorf("Authorization = %q, want %q", cap.auth, want)
	}
}

func TestNoAuthHeaderWhenTokenUnset(t *testing.T) {
	srv, cap := newFakeFGA(t, http.StatusOK, `{"allowed":true}`)

	if _, err := mustClient(t, enabledConfig(srv.URL)).Check(context.Background(), "u", "m", "ssh"); err != nil {
		t.Fatalf("Check: %v", err)
	}
	if cap.auth != "" {
		t.Errorf("Authorization = %q, want it unset", cap.auth)
	}
}

// --- error paths --------------------------------------------------------------

// A non-2xx must surface as an error and never as a silent "allowed=false",
// so an OpenFGA outage is visibly broken rather than quietly denying everyone.
func TestCheckSurfacesNon2xxAsError(t *testing.T) {
	for _, status := range []int{http.StatusBadRequest, http.StatusUnauthorized, http.StatusInternalServerError} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			srv, _ := newFakeFGA(t, status, `{"code":"boom","message":"upstream failed"}`)

			allowed, err := mustClient(t, enabledConfig(srv.URL)).Check(context.Background(), "u", "m", "ssh")
			if err == nil {
				t.Fatal("Check succeeded on an error status, want an error")
			}
			if allowed {
				t.Error("Check = true alongside an error, want false")
			}
			// The upstream body is the only diagnostic an operator gets.
			if !strings.Contains(err.Error(), "upstream failed") {
				t.Errorf("error = %v, want it to include the upstream response body", err)
			}
		})
	}
}

func TestGrantsSurfaceNon2xxAsError(t *testing.T) {
	t.Run("write", func(t *testing.T) {
		srv, _ := newFakeFGA(t, http.StatusInternalServerError, `nope`)
		if err := mustClient(t, enabledConfig(srv.URL)).WriteGrant(context.Background(), "u", "m", "ssh"); err == nil {
			t.Error("WriteGrant succeeded on a 500, want an error")
		}
	})

	t.Run("delete", func(t *testing.T) {
		srv, _ := newFakeFGA(t, http.StatusInternalServerError, `nope`)
		if err := mustClient(t, enabledConfig(srv.URL)).DeleteGrant(context.Background(), "u", "m", "ssh"); err == nil {
			t.Error("DeleteGrant succeeded on a 500, want an error")
		}
	})
}

func TestCheckFailsOnMalformedJSONResponse(t *testing.T) {
	srv, _ := newFakeFGA(t, http.StatusOK, `{"allowed":`)

	if _, err := mustClient(t, enabledConfig(srv.URL)).Check(context.Background(), "u", "m", "ssh"); err == nil {
		t.Error("Check succeeded on a truncated JSON body, want a decode error")
	}
}

// A 2xx with an empty body is fine for the grant calls, which pass out == nil.
func TestGrantsAcceptEmptySuccessBody(t *testing.T) {
	srv, _ := newFakeFGA(t, http.StatusOK, ``)

	if err := mustClient(t, enabledConfig(srv.URL)).WriteGrant(context.Background(), "u", "m", "ssh"); err != nil {
		t.Errorf("WriteGrant on an empty 200 body: %v", err)
	}
}

func TestCheckFailsWhenServerUnreachable(t *testing.T) {
	srv, _ := newFakeFGA(t, http.StatusOK, `{"allowed":true}`)
	url := srv.URL
	srv.Close() // nothing is listening now

	o := mustClient(t, enabledConfig(url))
	if _, err := o.Check(context.Background(), "u", "m", "ssh"); err == nil {
		t.Fatal("Check succeeded against a closed server, want a transport error")
	} else if !strings.Contains(err.Error(), "openfga request") {
		t.Errorf("error = %v, want it wrapped as an openfga request failure", err)
	}
}

func TestCheckHonoursCancelledContext(t *testing.T) {
	srv, cap := newFakeFGA(t, http.StatusOK, `{"allowed":true}`)

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancelled before the call

	if _, err := mustClient(t, enabledConfig(srv.URL)).Check(ctx, "u", "m", "ssh"); err == nil {
		t.Error("Check succeeded with a cancelled context, want an error")
	}
	if cap.called != 0 {
		t.Errorf("server was called %d times despite a cancelled context, want 0", cap.called)
	}
}

// --- interface conformance ----------------------------------------------------

func TestOpenFGASatisfiesAuthorizer(t *testing.T) {
	var _ Authorizer = (*OpenFGA)(nil)
}
