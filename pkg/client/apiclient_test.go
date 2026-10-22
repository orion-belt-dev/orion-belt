package client

import (
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"
)

func setPassword(t *testing.T, answer string, err error) {
	t.Helper()
	prev := readPassword
	readPassword = func() ([]byte, error) { return []byte(answer), err }
	t.Cleanup(func() { readPassword = prev })
}

func loadClient(t *testing.T, api *apiStub) *APIClient {
	t.Helper()
	cfg, _ := clientConfig(t, nil, api)
	c, err := LoadAPIClient(cfg, "", quietLogger)
	if err != nil {
		t.Fatalf("LoadAPIClient: %v", err)
	}
	return c
}

func TestLoadAPIClient_ResolvesEndpointKeyAndUser(t *testing.T) {
	api := newAPIStub(t)
	cfg, _ := clientConfig(t, nil, api)

	c, err := LoadAPIClient(cfg, "override", quietLogger)
	if err != nil || c.apiKey != "issued" {
		t.Fatalf("LoadAPIClient = %+v, %v", c, err)
	}

	cfg.Auth.User = ""
	t.Setenv("USER", "")
	if _, err := LoadAPIClient(cfg, "", nil); err == nil || !strings.Contains(err.Error(), "username not configured") {
		t.Errorf("no username = %v", err)
	}
	t.Setenv("USER", "envuser")
	if _, err := LoadAPIClient(cfg, "", nil); err != nil {
		t.Errorf("$USER fallback = %v", err)
	}

	bad := filepath.Join(t.TempDir(), "bad")
	_ = os.WriteFile(bad, []byte("garbage"), 0o600)
	cfg.Auth.KeyFile = bad
	if _, err := LoadAPIClient(cfg, "", nil); err == nil || !strings.Contains(err.Error(), "parse private key") {
		t.Errorf("bad key = %v", err)
	}

	cfg.Server.APIEndpoint = ""
	cfg.Server.Host = "127.0.0.1:1/unreachable"
	cfg.Auth.KeyFile, _ = writeKey(t, t.TempDir())
	if _, err := LoadAPIClient(cfg, "", nil); err == nil {
		t.Error("LoadAPIClient against the default endpoint of a bogus host succeeded")
	}
}

func TestAuthenticate_MFARetry(t *testing.T) {
	api := newAPIStub(t)
	api.mfa = true
	_ = withStderrDiscard(t)

	setPassword(t, " 123456 ", nil)
	c := loadClient(t, api)
	if c.apiKey != "issued" || api.totpSeen != "123456" {
		t.Errorf("after MFA retry key=%q totp=%q", c.apiKey, api.totpSeen)
	}

	cfg, _ := clientConfig(t, nil, api)
	setPassword(t, "", nil)
	if _, err := LoadAPIClient(cfg, "", nil); err == nil || !strings.Contains(err.Error(), "mfa code required") {
		t.Errorf("empty code = %v", err)
	}
	setPassword(t, "", errors.New("no tty"))
	if _, err := LoadAPIClient(cfg, "", nil); err == nil || !strings.Contains(err.Error(), "mfa required") {
		t.Errorf("prompt failure = %v", err)
	}
}

func withStderrDiscard(t *testing.T) func() {
	t.Helper()
	prev := os.Stderr
	devnull, _ := os.Open(os.DevNull)
	os.Stderr = devnull
	restore := func() { os.Stderr = prev; devnull.Close() }
	t.Cleanup(restore)
	return restore
}

func TestAuthenticate_Failures(t *testing.T) {
	api := newAPIStub(t)
	cfg, _ := clientConfig(t, nil, api)

	api.fail["/api/v1/public/auth/challenge"] = http.StatusServiceUnavailable
	if _, err := LoadAPIClient(cfg, "", nil); err == nil || !strings.Contains(err.Error(), "login challenge") {
		t.Errorf("challenge failure = %v", err)
	}
	delete(api.fail, "/api/v1/public/auth/challenge")

	api.fail["/api/v1/public/login/key"] = http.StatusUnauthorized
	if _, err := LoadAPIClient(cfg, "", nil); err == nil || !strings.Contains(err.Error(), "status 401") {
		t.Errorf("login failure = %v", err)
	}

	c := NewUnauthenticatedAPIClient(api.URL, nil)
	if err := c.authenticate("alice", failingSigner{genTestSigner(t)}); err == nil || !strings.Contains(err.Error(), "sign login challenge") {
		t.Errorf("signing failure = %v", err)
	}
}

type failingSigner struct{ ssh.Signer }

func (failingSigner) Sign(io.Reader, []byte) (*ssh.Signature, error) {
	return nil, errors.New("token unplugged")
}

func TestAuthenticate_NoAPIKeyIssued(t *testing.T) {
	srv := newScriptedServer(t, map[string]string{
		"/api/v1/public/auth/challenge": `{"challenge":"nonce"}`,
		"/api/v1/public/login/key":      `{}`,
	})
	_, signer := writeKey(t, t.TempDir())
	if _, err := NewAPIClient(srv.URL, "alice", signer, nil); err == nil || !strings.Contains(err.Error(), "no API key returned") {
		t.Errorf("NewAPIClient = %v", err)
	}
}

func TestAPIClient_AdminAndCAEndpoints(t *testing.T) {
	api := newAPIStub(t)
	c := loadClient(t, api)

	if reqs, err := c.ListPendingAccessRequests(); err != nil || len(reqs) != 1 {
		t.Errorf("ListPendingAccessRequests = %v, %v", reqs, err)
	}
	if err := c.ApproveAccessRequest("req-1", "admin"); err != nil {
		t.Errorf("Approve = %v", err)
	}
	if err := c.RejectAccessRequest("req-1", "admin"); err != nil {
		t.Errorf("Reject = %v", err)
	}
	if ca, err := c.ExportCA(); err != nil || ca.HostCA != "ssh-ed25519 HOST" {
		t.Errorf("ExportCA = %+v, %v", ca, err)
	}
	if certs, err := c.ListSSHCertificates("user"); err != nil || len(certs) != 1 {
		t.Errorf("ListSSHCertificates = %v, %v", certs, err)
	}
	if _, err := c.ListSSHCertificates(""); err != nil {
		t.Errorf("ListSSHCertificates(all) = %v", err)
	}
	if err := c.RevokeSSHCertificate("7", "lost"); err != nil {
		t.Errorf("Revoke = %v", err)
	}
	if code, err := c.RequestBrowserBootstrap(); err != nil || code.Code != "abc" {
		t.Errorf("RequestBrowserBootstrap = %+v, %v", code, err)
	}

	want := []string{
		"GET /api/v1/admin/access-requests/pending",
		"POST /api/v1/admin/access-requests/req-1/approve",
		"POST /api/v1/admin/access-requests/req-1/reject",
		"GET /api/v1/admin/ca/export",
		"GET /api/v1/admin/ssh-certificates?cert_type=user",
		"GET /api/v1/admin/ssh-certificates",
		"POST /api/v1/admin/ssh-certificates/7/revoke",
		"POST /api/v1/auth/browser-bootstrap",
	}
	seen := strings.Join(api.seen(), "\n")
	for _, w := range want {
		if !strings.Contains(seen, w) {
			t.Errorf("no request %q in:\n%s", w, seen)
		}
	}
}

func TestAPIClient_ErrorsSurface(t *testing.T) {
	api := newAPIStub(t)
	c := loadClient(t, api)
	for _, path := range []string{
		"/api/v1/machines", "/api/v1/access-requests", "/api/v1/access-requests/req-1",
		"/api/v1/admin/access-requests/pending", "/api/v1/ssh-cert/ca", "/api/v1/ssh-cert",
		"/api/v1/admin/ca/export", "/api/v1/admin/ssh-certificates", "/api/v1/auth/browser-bootstrap",
	} {
		api.fail[path] = http.StatusInternalServerError
	}
	calls := map[string]func() error{
		"ListMachines":              func() error { _, err := c.ListMachines(); return err },
		"GetMachineByName":          func() error { _, err := c.GetMachineByName("x"); return err },
		"CreateAccessRequest":       func() error { _, err := c.CreateAccessRequest(nil); return err },
		"GetAccessRequest":          func() error { _, err := c.GetAccessRequest("req-1"); return err },
		"ListPendingAccessRequests": func() error { _, err := c.ListPendingAccessRequests(); return err },
		"GetTrustedCA":              func() error { _, err := c.GetTrustedCA(); return err },
		"IssueUserCert":             func() error { _, err := c.IssueUserCert("k", 0); return err },
		"ExportCA":                  func() error { _, err := c.ExportCA(); return err },
		"ListSSHCertificates":       func() error { _, err := c.ListSSHCertificates(""); return err },
		"RequestBrowserBootstrap":   func() error { _, err := c.RequestBrowserBootstrap(); return err },
	}
	for name, call := range calls {
		if err := call(); err == nil || !strings.Contains(err.Error(), "status 500") {
			t.Errorf("%s = %v, want the API error", name, err)
		}
	}
}

func TestAPIClient_RequestPlumbingErrors(t *testing.T) {
	c := NewUnauthenticatedAPIClient("http://127.0.0.1:1", nil)
	unencodable := map[string]any{"ch": make(chan int)}
	if err := c.doRequest("POST", "/x", unencodable, nil); err == nil || !strings.Contains(err.Error(), "marshal") {
		t.Errorf("doRequest marshal = %v", err)
	}
	if err := c.doRequestNoAuth("POST", "/x", unencodable, nil); err == nil || !strings.Contains(err.Error(), "marshal") {
		t.Errorf("doRequestNoAuth marshal = %v", err)
	}
	if err := c.doRequest("BAD METHOD", "/x", nil, nil); err == nil || !strings.Contains(err.Error(), "create request") {
		t.Errorf("doRequest method = %v", err)
	}
	if err := c.doRequestNoAuth("BAD METHOD", "/x", nil, nil); err == nil || !strings.Contains(err.Error(), "create request") {
		t.Errorf("doRequestNoAuth method = %v", err)
	}
	if err := c.doRequest("GET", "/x", nil, nil); err == nil || !strings.Contains(err.Error(), "request failed") {
		t.Errorf("doRequest network = %v", err)
	}

	srv := newScriptedServer(t, map[string]string{"/x": "not json"})
	c = NewUnauthenticatedAPIClient(srv.URL, nil)
	var out map[string]any
	if err := c.doRequest("GET", "/x", nil, &out); err == nil || !strings.Contains(err.Error(), "parse response") {
		t.Errorf("doRequest parse = %v", err)
	}
	if err := c.doRequestNoAuth("GET", "/x", nil, &out); err == nil || !strings.Contains(err.Error(), "parse response") {
		t.Errorf("doRequestNoAuth parse = %v", err)
	}
}

func TestLoginWithPasswordUsesSessionToken(t *testing.T) {
	api := newAPIStub(t)
	c := NewUnauthenticatedAPIClient(api.URL, quietLogger)
	res, err := c.LoginWithPassword("alice", "pw", "123456")
	if err != nil || res.SessionToken != "sess" || c.sessionToken != "sess" {
		t.Fatalf("LoginWithPassword = %+v, %v", res, err)
	}
	if _, err := c.RequestBrowserBootstrap(); err != nil {
		t.Errorf("bootstrap with session token = %v", err)
	}

	empty := newScriptedServer(t, map[string]string{"/api/v1/public/login/password": `{}`})
	c = NewUnauthenticatedAPIClient(empty.URL, nil)
	if _, err := c.LoginWithPassword("a", "b", "c"); err == nil || !strings.Contains(err.Error(), "no session") {
		t.Errorf("no session = %v", err)
	}
	api.fail["/api/v1/public/login/password"] = http.StatusUnauthorized
	c = NewUnauthenticatedAPIClient(api.URL, nil)
	if _, err := c.LoginWithPassword("a", "b", "c"); err == nil {
		t.Error("rejected password login succeeded")
	}
}

func TestIsMFARequired(t *testing.T) {
	for msg, want := range map[string]bool{
		`API error (status 401): {"mfa_required":true}`:  true,
		`API error (status 401): {"mfa_required": true}`: true,
		`API error (status 401): {"error":"nope"}`:       false,
	} {
		if got := isMFARequired(errors.New(msg)); got != want {
			t.Errorf("isMFARequired(%q) = %v", msg, got)
		}
	}
	if isMFARequired(nil) {
		t.Error("nil error reported as MFA required")
	}
}

// --- certificate signer -------------------------------------------------------------

func TestLoadSigner_RefreshesAndCachesCertificate(t *testing.T) {
	api := newAPIStub(t)
	cfg, signer := clientConfig(t, nil, api)
	authority := genTestSigner(t)
	cert := selfSignedCert(t, signer, authority, time.Now().Add(-time.Minute), time.Now().Add(12*time.Hour))
	api.caEnabled = true
	api.certLine = string(ssh.MarshalAuthorizedKey(cert))

	got, err := LoadSigner(cfg, "", quietLogger)
	if err != nil {
		t.Fatalf("LoadSigner: %v", err)
	}
	if _, ok := got.PublicKey().(*ssh.Certificate); !ok {
		t.Fatalf("LoadSigner returned %T, want a certificate signer", got.PublicKey())
	}
	if data, err := os.ReadFile(cfg.Auth.KeyFile + "-cert.pub"); err != nil || string(data) != api.certLine {
		t.Errorf("certificate not cached: %v", err)
	}

	api.caEnabled = false // a cached, still-valid cert is used without asking the server
	if got, _ := LoadSigner(cfg, "", quietLogger); got.PublicKey().Type() != cert.Type() {
		t.Errorf("cached cert not reused: %s", got.PublicKey().Type())
	}
}

func TestLoadSigner_FallsBackToStaticKey(t *testing.T) {
	cases := map[string]func(api *apiStub, key ssh.Signer){
		"ca disabled":    func(api *apiStub, _ ssh.Signer) { api.caEnabled = false },
		"ca check fails": func(api *apiStub, _ ssh.Signer) { api.fail["/api/v1/ssh-cert/ca"] = 500 },
		"issuance fails": func(api *apiStub, _ ssh.Signer) { api.caEnabled = true; api.fail["/api/v1/ssh-cert"] = 500 },
		"garbage cert":   func(api *apiStub, _ ssh.Signer) { api.caEnabled = true; api.certLine = "not a key" },
		"plain key, no cert": func(api *apiStub, key ssh.Signer) {
			api.caEnabled = true
			api.certLine = string(ssh.MarshalAuthorizedKey(key.PublicKey()))
		},
	}
	for name, setup := range cases {
		t.Run(name, func(t *testing.T) {
			api := newAPIStub(t)
			cfg, signer := clientConfig(t, nil, api)
			setup(api, signer)

			got, err := LoadSigner(cfg, "", quietLogger)
			if err != nil {
				t.Fatalf("LoadSigner: %v", err)
			}
			if _, isCert := got.PublicKey().(*ssh.Certificate); isCert {
				t.Error("got a certificate signer, want the static key")
			}
		})
	}
}

func TestLoadSigner_CertCacheWriteFailureIsLogged(t *testing.T) {
	api := newAPIStub(t)
	cfg, signer := clientConfig(t, nil, api)
	cert := selfSignedCert(t, signer, genTestSigner(t), time.Now().Add(-time.Minute), time.Now().Add(12*time.Hour))
	api.caEnabled = true
	api.certLine = string(ssh.MarshalAuthorizedKey(cert))
	// A directory where the cache file would go makes the write fail.
	if err := os.Mkdir(cfg.Auth.KeyFile+"-cert.pub", 0o700); err != nil {
		t.Fatal(err)
	}
	got, err := LoadSigner(cfg, "", quietLogger)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := got.PublicKey().(*ssh.Certificate); !ok {
		t.Error("an uncacheable cert should still be used for this connection")
	}
}

func TestLoadSigner_KeyErrors(t *testing.T) {
	cfg, _ := clientConfig(t, nil, nil)
	cfg.Auth.KeyFile = filepath.Join(t.TempDir(), "absent")
	if _, err := LoadSigner(cfg, "", nil); err == nil || !strings.Contains(err.Error(), "read SSH key") {
		t.Errorf("missing key = %v", err)
	}
	bad := filepath.Join(t.TempDir(), "bad")
	_ = os.WriteFile(bad, []byte("garbage"), 0o600)
	cfg.Auth.KeyFile = bad
	if _, err := LoadSigner(cfg, "", nil); err == nil || !strings.Contains(err.Error(), "parse private key") {
		t.Errorf("bad key = %v", err)
	}
}

func TestLoadCachedCertSigner_RejectsNonCerts(t *testing.T) {
	key := genTestSigner(t)
	dir := t.TempDir()
	garbage := filepath.Join(dir, "garbage")
	_ = os.WriteFile(garbage, []byte("nope"), 0o600)
	plain := filepath.Join(dir, "plain")
	_ = os.WriteFile(plain, ssh.MarshalAuthorizedKey(key.PublicKey()), 0o600)
	for _, p := range []string{garbage, plain} {
		if loadCachedCertSigner(p, key) != nil {
			t.Errorf("loadCachedCertSigner(%s) accepted a non-certificate", filepath.Base(p))
		}
	}
}

// newScriptedServer answers each listed path with its body and 404s the rest.
func newScriptedServer(t *testing.T, routes map[string]string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, ok := routes[r.URL.Path]
		if !ok {
			http.NotFound(w, r)
			return
		}
		_, _ = io.WriteString(w, body)
	}))
	t.Cleanup(srv.Close)
	return srv
}
