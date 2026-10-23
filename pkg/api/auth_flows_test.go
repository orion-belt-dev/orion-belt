package api

import (
	"context"
	"encoding/base64"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/pquerna/otp/totp"
	"golang.org/x/crypto/ssh"

	"github.com/orion-belt-dev/orion-belt/pkg/auth"
	"github.com/orion-belt-dev/orion-belt/pkg/common"
)

// keyUser creates an account whose stored public key matches a fresh signer.
func (e *env) keyUser(name string) (*common.User, ssh.Signer, string) {
	e.t.Helper()
	signer := genSigner(e.t)
	pubLine := string(ssh.MarshalAuthorizedKey(signer.PublicKey()))
	u := common.NewUser(name, name+"@example.com", pubLine, false)
	if err := e.store.CreateUser(context.Background(), u); err != nil {
		e.t.Fatal(err)
	}
	return u, signer, pubLine
}

// signedLogin posts a challenge-signed login to path.
func (e *env) signedLogin(path, username string, signer ssh.Signer, pubLine, totpCode string) resp {
	e.t.Helper()
	ch := e.do("POST", "/api/v1/public/auth/challenge", "", map[string]any{"username": username}).obj(e.t)["challenge"].(string)
	sig, err := signer.Sign(nil, []byte(ch))
	if err != nil {
		e.t.Fatal(err)
	}
	return e.do("POST", path, "", map[string]any{
		"username": username, "public_key": pubLine, "challenge": ch,
		"signature_format": sig.Format, "signature": base64.StdEncoding.EncodeToString(sig.Blob), "totp_code": totpCode,
	})
}

func code(t *testing.T, secret string) string {
	t.Helper()
	c, err := totp.GenerateCode(secret, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	return c
}

// enableMFA stores a TOTP secret and backup codes on u directly.
func (e *env) enableMFA(u *common.User) (secret string, backup []string) {
	e.t.Helper()
	key, err := auth.GenerateTOTPSecret("Orion-Belt", u.Username)
	if err != nil {
		e.t.Fatal(err)
	}
	codes, hashes, err := auth.GenerateBackupCodes()
	if err != nil {
		e.t.Fatal(err)
	}
	u.MFAEnabled, u.TOTPSecret, u.BackupCodesHash = true, key.Secret(), hashes
	if err := e.store.UpdateUser(context.Background(), u); err != nil {
		e.t.Fatal(err)
	}
	return key.Secret(), codes
}

// --- API keys -----------------------------------------------------------------------

func TestAPIKeys_Endpoints(t *testing.T) {
	e := newEnv(t, nil)

	e.expect(e.do("POST", "/api/v1/api-keys", e.aliceKey, `{`), 400)
	r := e.do("POST", "/api/v1/api-keys", e.aliceKey, map[string]any{"name": "ci", "expires_in": 7})
	e.expect(r, 201)
	created := r.obj(t)
	if created["expires_at"] == nil || !strings.HasPrefix(created["api_key"].(string), created["key_prefix"].(string)) {
		t.Errorf("created key = %v", created)
	}
	e.expect(e.do("GET", "/api/v1/auth/me", created["api_key"].(string), nil), 200)
	r = e.do("POST", "/api/v1/api-keys", e.aliceKey, map[string]any{"name": "forever"})
	e.expect(r, 201)
	forever := r.obj(t)["id"].(string)

	var listed struct {
		APIKeys []map[string]any `json:"api_keys"`
	}
	e.do("GET", "/api/v1/api-keys", e.aliceKey, nil).json(t, &listed)
	if len(listed.APIKeys) != 3 { // the harness key plus two
		t.Errorf("listed %d keys", len(listed.APIKeys))
	}

	id := created["id"].(string)
	e.expect(e.do("POST", "/api/v1/api-keys/missing/revoke", e.aliceKey, nil), 404)
	e.expect(e.do("POST", "/api/v1/api-keys/"+id+"/revoke", e.bobKey, nil), 403)
	e.expect(e.do("POST", "/api/v1/api-keys/"+id+"/revoke", e.aliceKey, nil), 200)
	e.expect(e.do("GET", "/api/v1/auth/me", created["api_key"].(string), nil), 401)

	e.expect(e.do("DELETE", "/api/v1/api-keys/missing", e.aliceKey, nil), 404)
	e.expect(e.do("DELETE", "/api/v1/api-keys/"+forever, e.bobKey, nil), 403)
	e.expect(e.do("DELETE", "/api/v1/api-keys/"+forever, e.adminKey, nil), 200)
	if _, err := e.store.GetAPIKey(context.Background(), forever); err == nil {
		t.Error("deleted key still stored")
	}
}

// --- key login: session and JWT ----------------------------------------------------------

func TestLogin_SessionAndJWT(t *testing.T) {
	e := newEnv(t, nil)
	carol, signer, pubLine := e.keyUser("carol")
	broken := common.NewUser("broken", "b@x", "not a key", false)
	_ = e.store.CreateUser(context.Background(), broken)

	for _, path := range []string{"/api/v1/public/login", "/api/v1/public/login/token"} {
		t.Run(path, func(t *testing.T) {
			e.expect(e.do("POST", path, "", `{`), 400)
			e.expect(e.signedLogin(path, "nobody", signer, pubLine, ""), 401)
			e.expect(e.signedLogin(path, "broken", signer, pubLine, ""), 401)
			e.expect(e.signedLogin(path, "carol", signer, "garbage", ""), 400)
			e.expect(e.signedLogin(path, "carol", signer, testPubKey, ""), 401)
			e.expect(e.signedLogin(path, "carol", genSigner(t), pubLine, ""), 401)
		})
	}

	r := e.signedLogin("/api/v1/public/login", "carol", signer, pubLine, "")
	e.expect(r, 200)
	var login LoginResponse
	r.json(t, &login)
	if login.SessionToken == "" || login.AccessToken == "" || login.User.ID != carol.ID || !login.User.MustSetPassword {
		t.Errorf("login = %+v", login)
	}
	e.expect(e.do("GET", "/api/v1/auth/me", "", nil, "X-Session-Token", login.SessionToken), 200)
	e.expect(e.do("GET", "/api/v1/auth/me", "", nil, "Authorization", "Bearer "+login.AccessToken), 200)
	if !e.audited("auth.login") {
		t.Error("login not audited")
	}

	r = e.signedLogin("/api/v1/public/login/token", "carol", signer, pubLine, "")
	e.expect(r, 200)
	tok := r.obj(t)
	if tok["token_type"] != "Bearer" {
		t.Errorf("token response = %v", tok)
	}
	e.expect(e.do("GET", "/api/v1/auth/me", "", nil, "Authorization", "Bearer "+tok["access_token"].(string)), 200)
	if !e.audited("auth.login.token") {
		t.Error("token login not audited")
	}
}

func TestLogin_WithoutJWTSecret(t *testing.T) {
	e := newEnv(t, func(o *Options) { o.JWTSecret = "" })
	_, signer, pubLine := e.keyUser("carol")

	r := e.signedLogin("/api/v1/public/login", "carol", signer, pubLine, "")
	e.expect(r, 200)
	if r.obj(t)["access_token"] != nil {
		t.Error("access token issued without a JWT secret")
	}
	e.expect(e.signedLogin("/api/v1/public/login/token", "carol", signer, pubLine, ""), 503)
}

func TestLogout(t *testing.T) {
	e := newEnv(t, nil)
	ctx := context.Background()
	newToken := func() string {
		_, tok, err := e.srv.authService.CreateSession(ctx, e.alice.ID, "1.1.1.1", "ua", time.Hour)
		if err != nil {
			t.Fatal(err)
		}
		return tok
	}

	e.expect(e.do("POST", "/api/v1/logout", e.aliceKey, nil), 400)

	tok := newToken()
	e.expect(e.do("POST", "/api/v1/logout", "", nil, "X-Session-Token", tok), 200)
	e.expect(e.do("GET", "/api/v1/auth/me", "", nil, "X-Session-Token", tok), 401)

	tok = newToken()
	e.expect(e.do("POST", "/api/v1/logout", "", nil, "Cookie", "session_token="+tok), 200)
	e.expect(e.do("GET", "/api/v1/auth/me", "", nil, "Cookie", "session_token="+tok), 401)

	// Authenticated through ?token= only: there is no header or cookie to end.
	e.expect(e.do("POST", "/api/v1/logout?token="+newToken(), "", nil), 400)
}

// --- MFA ------------------------------------------------------------------------------

func TestMFA_EnrollConfirmDisable(t *testing.T) {
	e := newEnv(t, nil)

	status := e.do("GET", "/api/v1/mfa/status", e.aliceKey, nil).obj(t)
	if status["mfa_enabled"] != false || status["mfa_required"] != false {
		t.Errorf("status = %v", status)
	}

	e.expect(e.do("POST", "/api/v1/mfa/confirm", e.aliceKey, `{`), 400)
	e.expect(e.do("POST", "/api/v1/mfa/confirm", e.aliceKey, map[string]any{"code": "123456"}), 400) // nothing pending

	r := e.do("POST", "/api/v1/mfa/enroll", e.aliceKey, map[string]any{})
	e.expect(r, 200)
	enroll := r.obj(t)
	secret := enroll["secret"].(string)
	if !strings.HasPrefix(enroll["otpauth_url"].(string), "otpauth://totp/") || len(enroll["backup_codes"].([]any)) != 8 {
		t.Errorf("enroll = %v", enroll)
	}
	e.expect(e.do("POST", "/api/v1/mfa/confirm", e.aliceKey, map[string]any{"code": "000000"}), 401)
	e.expect(e.do("POST", "/api/v1/mfa/confirm", e.aliceKey, map[string]any{"code": code(t, secret)}), 200)
	e.expect(e.do("POST", "/api/v1/mfa/enroll", e.aliceKey, map[string]any{}), 409)

	e.expect(e.do("POST", "/api/v1/mfa/disable", e.aliceKey, `{`), 400)
	e.expect(e.do("POST", "/api/v1/mfa/disable", e.bobKey, map[string]any{"code": "1"}), 400) // not enabled
	e.expect(e.do("POST", "/api/v1/mfa/disable", e.aliceKey, map[string]any{"code": "000000"}), 401)

	// Disabling TOTP is refused while it guards a password.
	u, _ := e.store.GetUser(context.Background(), e.alice.ID)
	u.PasswordHash, _ = auth.HashPassword("correct horse battery")
	_ = e.store.UpdateUser(context.Background(), u)
	e.expect(e.do("POST", "/api/v1/mfa/disable", e.aliceKey, map[string]any{"code": code(t, secret)}), 409)
	u.PasswordHash = ""
	_ = e.store.UpdateUser(context.Background(), u)

	e.expect(e.do("POST", "/api/v1/mfa/disable", e.aliceKey, map[string]any{"code": code(t, secret)}), 200)
	if u, _ := e.store.GetUser(context.Background(), e.alice.ID); u.MFAEnabled || u.TOTPSecret != "" {
		t.Errorf("after disable = %+v", u)
	}
}

func TestMFARequired_KeyLogin(t *testing.T) {
	e := newEnv(t, func(o *Options) { o.MFARequired = true })
	carol, signer, pubLine := e.keyUser("carol")
	login := func(totpCode string) resp {
		return e.signedLogin("/api/v1/public/login/key", "carol", signer, pubLine, totpCode)
	}

	r := login("")
	e.expect(r, 403)
	if r.obj(t)["mfa_enrollment_required"] != true {
		t.Errorf("unenrolled = %s", r.body)
	}

	secret, backup := e.enableMFA(carol)
	r = login("")
	e.expect(r, 401)
	if r.obj(t)["mfa_required"] != true {
		t.Errorf("no code = %s", r.body)
	}
	e.expect(login("000000"), 401)
	e.expect(login(code(t, secret)), 200)

	e.expect(login(backup[0]), 200)
	e.expect(login(backup[0]), 401) // backup codes are single use
	if s := e.do("GET", "/api/v1/mfa/status", e.aliceKey, nil).obj(t); s["mfa_required"] != true {
		t.Errorf("status = %v", s)
	}
}

// --- password + TOTP ---------------------------------------------------------------------

func TestPassword_SetLoginClear(t *testing.T) {
	e := newEnv(t, nil)
	set := func(key, pw, totpCode string) resp {
		return e.do("POST", "/api/v1/auth/password", key, map[string]any{"password": pw, "totp_code": totpCode})
	}

	e.expect(e.do("POST", "/api/v1/auth/password", e.aliceKey, `{`), 400)
	e.expect(set(e.aliceKey, "short", "1"), 400)
	e.expect(set(e.aliceKey, "correct horse battery", "123456"), 400) // no enrollment yet

	secret := e.do("POST", "/api/v1/mfa/enroll", e.aliceKey, map[string]any{}).obj(t)["secret"].(string)
	e.expect(set(e.aliceKey, "correct horse battery", "000000"), 401)
	r := set(e.aliceKey, "correct horse battery", code(t, secret))
	e.expect(r, 200)
	if r.obj(t)["mfa_enabled"] != true {
		t.Errorf("set password = %s", r.body)
	}
	e.expect(set(e.aliceKey, "another long password", "000000"), 401)
	e.expect(set(e.aliceKey, "another long password", code(t, secret)), 200)
	if !e.audited("auth.password.set") {
		t.Error("password set not audited")
	}

	pwLogin := func(body map[string]any) resp {
		return e.do("POST", "/api/v1/public/login/password", "", body)
	}
	e.expect(pwLogin(map[string]any{}), 400)
	e.expect(pwLogin(map[string]any{"username": "nobody", "password": "x"}), 401)
	e.expect(pwLogin(map[string]any{"username": "alice", "password": "wrong password!"}), 401)

	step1 := pwLogin(map[string]any{"username": "alice", "password": "another long password"})
	e.expect(step1, 200)
	ticket := step1.obj(t)["ticket"].(string)
	e.expect(pwLogin(map[string]any{"username": "alice", "password": "another long password", "totp_code": code(t, secret), "ticket": "forged"}), 401)
	e.expect(pwLogin(map[string]any{"username": "alice", "password": "another long password", "totp_code": "000000", "ticket": ticket}), 401)

	ticket = pwLogin(map[string]any{"username": "alice", "password": "another long password"}).obj(t)["ticket"].(string)
	r = pwLogin(map[string]any{"username": "alice", "password": "another long password", "totp_code": code(t, secret), "ticket": ticket})
	e.expect(r, 200)
	sess := r.obj(t)
	e.expect(e.do("GET", "/api/v1/auth/me", "", nil, "X-Session-Token", sess["session_token"].(string)), 200)
	// CLI one-shot: password and code together, no ticket.
	e.expect(pwLogin(map[string]any{"username": "alice", "password": "another long password", "totp_code": code(t, secret)}), 200)

	clear := func(key, totpCode string) resp {
		return e.do("DELETE", "/api/v1/auth/password", key, map[string]any{"totp_code": totpCode})
	}
	e.expect(e.do("DELETE", "/api/v1/auth/password", e.aliceKey, `{`), 400)
	e.expect(clear(e.bobKey, "1"), 400) // no password
	e.expect(clear(e.aliceKey, "000000"), 401)
	e.expect(clear(e.aliceKey, code(t, secret)), 200)
	e.expect(pwLogin(map[string]any{"username": "alice", "password": "another long password"}), 401)
}

func TestPassword_RequiresTOTP(t *testing.T) {
	e := newEnv(t, nil)
	// A password stored without TOTP (legacy data) cannot be used or cleared.
	u, _ := e.store.GetUser(context.Background(), e.bob.ID)
	u.PasswordHash, _ = auth.HashPassword("correct horse battery")
	_ = e.store.UpdateUser(context.Background(), u)

	r := e.do("POST", "/api/v1/public/login/password", "", map[string]any{"username": "bob", "password": "correct horse battery"})
	e.expect(r, 403)
	e.expect(e.do("DELETE", "/api/v1/auth/password", e.bobKey, map[string]any{"totp_code": "1"}), 400)

	// Backup codes count as the second factor.
	_, backup := e.enableMFA(u)
	r = e.do("POST", "/api/v1/public/login/password", "", map[string]any{"username": "bob", "password": "correct horse battery", "totp_code": backup[0]})
	e.expect(r, 200)
	e.expect(e.do("POST", "/api/v1/auth/password", e.bobKey, map[string]any{"password": "brand new password", "totp_code": backup[1]}), 200)
	e.expect(e.do("DELETE", "/api/v1/auth/password", e.bobKey, map[string]any{"totp_code": backup[2]}), 200)
}

func TestPasswordTicketStore(t *testing.T) {
	s := newPasswordTicketStore()
	id, err := s.issue("u1", "alice")
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := s.consume(id, "mallory"); ok {
		t.Error("ticket redeemed for another username")
	}
	if uid, ok := s.consume(id, "alice"); !ok || uid != "u1" {
		t.Errorf("consume = %q %v", uid, ok)
	}
	if _, ok := s.consume(id, "alice"); ok {
		t.Error("ticket redeemed twice")
	}

	stale, _ := s.issue("u2", "bob")
	s.mu.Lock()
	tk := s.tickets[stale]
	tk.expiresAt = time.Now().Add(-time.Second)
	s.tickets[stale] = tk
	s.mu.Unlock()
	if _, ok := s.consume(stale, "bob"); ok {
		t.Error("expired ticket redeemed")
	}
}

// --- agent control ---------------------------------------------------------------------

func TestAgentControl(t *testing.T) {
	e := newEnv(t, nil)
	e.agents.connected = []string{e.machine.ID}
	e.agents.output = []byte(`{"pong":true}`)

	var listed struct{ Agents []string }
	e.do("GET", "/api/v1/admin/agents/connected", e.operatorKey, nil).json(t, &listed)
	if len(listed.Agents) != 1 {
		t.Errorf("connected = %v", listed.Agents)
	}

	path := "/api/v1/admin/agents/" + e.machine.ID
	e.expect(e.do("POST", path+"/command", e.adminKey, `{`), 400)
	r := e.do("POST", path+"/command", e.adminKey, map[string]any{"command": "orion:ping"})
	e.expect(r, 200)
	if r.obj(t)["output"] != `{"pong":true}` {
		t.Errorf("command = %s", r.body)
	}
	e.expect(e.do("POST", path+"/disconnect", e.adminKey, nil), 200)

	e.agents.err = errors.New("agent not connected")
	e.expect(e.do("POST", path+"/command", e.adminKey, map[string]any{"command": "orion:ping"}), 502)
	e.expect(e.do("POST", path+"/disconnect", e.adminKey, nil), 502)
	for _, a := range []string{"agent.command", "agent.disconnect"} {
		if !e.audited(a) {
			t.Errorf("%s not audited", a)
		}
	}
}

func TestAgentControl_WithoutCommander(t *testing.T) {
	e := newEnv(t, nil)
	e.srv.agentCommander = nil
	var listed struct{ Agents []string }
	e.do("GET", "/api/v1/admin/agents/connected", e.adminKey, nil).json(t, &listed)
	if listed.Agents == nil || len(listed.Agents) != 0 {
		t.Errorf("agents = %v, want an empty list", listed.Agents)
	}
	e.expect(e.do("POST", "/api/v1/admin/agents/x/command", e.adminKey, map[string]any{"command": "c"}), 503)
	e.expect(e.do("POST", "/api/v1/admin/agents/x/disconnect", e.adminKey, nil), 503)
}

// --- browser bootstrap -------------------------------------------------------------------

func TestBrowserBootstrap(t *testing.T) {
	e := newEnv(t, nil)
	redeem := func(body any) resp {
		return e.do("POST", "/api/v1/public/auth/browser-bootstrap/redeem", "", body)
	}

	r := e.do("POST", "/api/v1/auth/browser-bootstrap", e.aliceKey, nil)
	e.expect(r, 200)
	codeStr := r.obj(t)["code"].(string)
	if len(codeStr) != bootstrapCodeLen {
		t.Errorf("code %q has length %d", codeStr, len(codeStr))
	}

	e.expect(redeem(`{`), 400)
	e.expect(redeem(map[string]any{"code": "WRONGCODE2"}), 401)
	r = redeem(map[string]any{"code": codeStr})
	e.expect(r, 200)
	var login LoginResponse
	r.json(t, &login)
	if login.User.Username != "alice" || login.AccessToken == "" {
		t.Errorf("redeemed = %+v", login)
	}
	e.expect(e.do("GET", "/api/v1/auth/me", "", nil, "X-Session-Token", login.SessionToken), 200)
	e.expect(redeem(map[string]any{"code": codeStr}), 401) // single use

	// A code outlives its account: redeeming it must not resurrect access.
	orphan := e.do("POST", "/api/v1/auth/browser-bootstrap", e.bobKey, nil).obj(t)["code"].(string)
	_ = e.store.DeleteUser(context.Background(), e.bob.ID)
	e.expect(redeem(map[string]any{"code": orphan}), 401)
	if !e.audited("auth.browser_bootstrap.issue") || !e.audited("auth.browser_bootstrap.redeem") {
		t.Errorf("audit = %v", e.auditActions())
	}
}

func TestBootstrapStore_Expiry(t *testing.T) {
	b := newBootstrapStore()
	c, _, err := b.Issue("u1")
	if err != nil {
		t.Fatal(err)
	}
	b.mu.Lock()
	entry := b.codes[c]
	entry.expiresAt = time.Now().Add(-time.Second)
	b.codes[c] = entry
	b.mu.Unlock()
	if _, ok := b.Redeem(c); ok {
		t.Error("expired code redeemed")
	}

	stale, _, _ := b.Issue("u2")
	b.mu.Lock()
	e2 := b.codes[stale]
	e2.expiresAt = time.Now().Add(-time.Second)
	b.codes[stale] = e2
	b.mu.Unlock()
	_, _, _ = b.Issue("u3") // issuing collects expired codes
	b.mu.Lock()
	_, kept := b.codes[stale]
	b.mu.Unlock()
	if kept {
		t.Error("expired code not collected")
	}
}

// --- rate limits ---------------------------------------------------------------------------

func TestRateLimits(t *testing.T) {
	e := newEnv(t, func(o *Options) { o.RateLimitPerMinute = 2 })
	e.expect(e.do("GET", "/api/v1/auth/me", e.aliceKey, nil), 200)
	e.expect(e.do("GET", "/api/v1/auth/me", e.aliceKey, nil), 200)
	r := e.do("GET", "/api/v1/auth/me", e.aliceKey, nil)
	e.expect(r, 429)
	if r.hdr.Get("Retry-After") != "60" {
		t.Errorf("Retry-After = %q", r.hdr.Get("Retry-After"))
	}
	e.expect(e.do("GET", "/api/v1/auth/me", e.bobKey, nil), 200) // limits are per user

	// The bootstrap redeem endpoint shares the per-minute limiter.
	e.expect(e.do("POST", "/api/v1/public/auth/browser-bootstrap/redeem", "", map[string]any{"code": "x"}), 401)
	e.expect(e.do("POST", "/api/v1/public/auth/browser-bootstrap/redeem", "", map[string]any{"code": "x"}), 401)
	e.expect(e.do("POST", "/api/v1/public/auth/browser-bootstrap/redeem", "", map[string]any{"code": "x"}), 429)

	pub := newEnv(t, nil)
	var last resp
	for i := 0; i <= publicAuthRateLimitPerMinute; i++ {
		last = pub.do("POST", "/api/v1/public/auth/challenge", "", map[string]any{"username": "x"})
	}
	pub.expect(last, 429)
}
