package sdk

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"golang.org/x/crypto/ssh"
)

// loginServer issues a challenge and verifies the signed login exactly as
// the gateway does, so these tests check real proof-of-possession.
type loginServer struct {
	*httptest.Server
	challenge string // "" makes the challenge endpoint return an empty one
	issue     map[string]string
	totp      string
	authSeen  []string
}

func newLoginServer(t *testing.T) *loginServer {
	t.Helper()
	ls := &loginServer{challenge: "nonce-123", issue: map[string]string{
		"/api/v1/public/login/key":      `{"api_key":"issued-key"}`,
		"/api/v1/public/login":          `{"session_token":"issued-session","access_token":"issued-jwt"}`,
		"/api/v1/public/login/token":    `{"access_token":"issued-jwt"}`,
		"/api/v1/public/login/password": `{"session_token":"pw-session","access_token":"pw-jwt"}`,
	}}
	ls.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ls.authSeen = append(ls.authSeen, r.Header.Get("X-API-Key")+"|"+r.Header.Get("X-Session-Token")+"|"+r.Header.Get("Authorization"))
		var body map[string]string
		_ = json.NewDecoder(r.Body).Decode(&body)

		switch r.URL.Path {
		case "/api/v1/public/auth/challenge":
			_ = json.NewEncoder(w).Encode(map[string]string{"challenge": ls.challenge})
			return
		case "/api/v1/public/login/key", "/api/v1/public/login", "/api/v1/public/login/token":
			if err := verifyLogin(body, ls.challenge); err != nil {
				http.Error(w, `{"error":"`+err.Error()+`"}`, http.StatusUnauthorized)
				return
			}
			ls.totp = body["totp_code"]
		case "/api/v1/machines":
			_, _ = io.WriteString(w, `[{"id":"m1","name":"web-01"},{"id":"m2","name":"db-01"}]`)
			return
		}
		_, _ = io.WriteString(w, ls.issue[r.URL.Path])
	}))
	t.Cleanup(ls.Close)
	return ls
}

func verifyLogin(body map[string]string, challenge string) error {
	if body["challenge"] != challenge {
		return errors.New("challenge mismatch")
	}
	pub, _, _, _, err := ssh.ParseAuthorizedKey([]byte(body["public_key"]))
	if err != nil {
		return errors.New("bad public key")
	}
	blob, err := base64.StdEncoding.DecodeString(body["signature"])
	if err != nil {
		return errors.New("bad signature encoding")
	}
	return pub.Verify([]byte(challenge), &ssh.Signature{Format: body["signature_format"], Blob: blob})
}

func TestLoginWithSSHKey_SignsChallengeAndStoresAPIKey(t *testing.T) {
	ls := newLoginServer(t)
	c, _ := NewClient(ls.URL)
	ctx := context.Background()

	res, err := c.LoginWithSSHKey(ctx, "alice", newTestSigner(t), " 123456 ")
	if err != nil {
		t.Fatalf("LoginWithSSHKey: %v", err)
	}
	if res.APIKey != "issued-key" || c.apiKey != "issued-key" || ls.totp != "123456" {
		t.Errorf("result=%+v stored=%q totp=%q", res, c.apiKey, ls.totp)
	}
	if _, err := c.ListMachines(ctx); err != nil {
		t.Fatal(err)
	}
	if last := ls.authSeen[len(ls.authSeen)-1]; !strings.HasPrefix(last, "issued-key|") {
		t.Errorf("follow-up request auth = %q, want the issued API key", last)
	}
	for _, seen := range ls.authSeen[:2] {
		if seen != "||" {
			t.Errorf("login request carried credentials: %q", seen)
		}
	}
}

func TestLoginWithSSHSessionAndJWT(t *testing.T) {
	ls := newLoginServer(t)
	ctx := context.Background()

	c, _ := NewClient(ls.URL)
	res, err := c.LoginWithSSHSession(ctx, "alice", newTestSigner(t), "654321")
	if err != nil || res.SessionToken != "issued-session" || c.sessionToken != "issued-session" || c.bearerToken != "issued-jwt" || ls.totp != "654321" {
		t.Errorf("LoginWithSSHSession = %+v, %v; stored %q/%q", res, err, c.sessionToken, c.bearerToken)
	}

	c, _ = NewClient(ls.URL)
	jwt, err := c.LoginWithSSHJWT(ctx, "alice", newTestSigner(t), "111111")
	if err != nil || jwt.AccessToken != "issued-jwt" || c.bearerToken != "issued-jwt" || ls.totp != "111111" {
		t.Errorf("LoginWithSSHJWT = %+v, %v", jwt, err)
	}
}

func TestSSHLogins_FailureModes(t *testing.T) {
	ctx := context.Background()
	logins := map[string]func(c *Client, s ssh.Signer) error{
		"key":     func(c *Client, s ssh.Signer) error { _, err := c.LoginWithSSHKey(ctx, "alice", s, ""); return err },
		"session": func(c *Client, s ssh.Signer) error { _, err := c.LoginWithSSHSession(ctx, "alice", s, ""); return err },
		"jwt":     func(c *Client, s ssh.Signer) error { _, err := c.LoginWithSSHJWT(ctx, "alice", s, ""); return err },
	}
	for name, login := range logins {
		t.Run(name, func(t *testing.T) {
			ls := newLoginServer(t)
			c, _ := NewClient(ls.URL)

			if err := login(c, nil); err == nil || !strings.Contains(err.Error(), "signer is required") {
				t.Errorf("nil signer = %v", err)
			}
			if err := login(c, failingSigner{newTestSigner(t)}); err == nil || !strings.Contains(err.Error(), "sign challenge") {
				t.Errorf("failing signer = %v", err)
			}

			for path := range ls.issue {
				ls.issue[path] = `{}`
			}
			if err := login(c, newTestSigner(t)); err == nil || !strings.Contains(err.Error(), "returned") {
				t.Errorf("no credential issued = %v", err)
			}

			ls.challenge = ""
			if err := login(c, newTestSigner(t)); err == nil || !strings.Contains(err.Error(), "challenge") {
				t.Errorf("empty challenge = %v", err)
			}
			if c.apiKey != "" || c.sessionToken != "" || c.bearerToken != "" {
				t.Errorf("failed logins stored credentials: %q %q %q", c.apiKey, c.sessionToken, c.bearerToken)
			}
		})
	}
}

// failingSigner exposes a real public key but cannot sign.
type failingSigner struct{ ssh.Signer }

func (failingSigner) Sign(io.Reader, []byte) (*ssh.Signature, error) {
	return nil, errors.New("hardware token removed")
}

func TestLoginWithPassword(t *testing.T) {
	ls := newLoginServer(t)
	c, _ := NewClient(ls.URL)
	res, err := c.LoginWithPassword(context.Background(), "alice", "pw", "123456")
	if err != nil || res.SessionToken != "pw-session" || c.sessionToken != "pw-session" || c.bearerToken != "pw-jwt" {
		t.Fatalf("LoginWithPassword = %+v, %v", res, err)
	}

	ls.issue["/api/v1/public/login/password"] = `{"session_token":"only-session"}`
	c, _ = NewClient(ls.URL)
	if _, err := c.LoginWithPassword(context.Background(), "alice", "pw", ""); err != nil || c.bearerToken != "" {
		t.Errorf("session-only login: err=%v bearer=%q", err, c.bearerToken)
	}

	ls.issue["/api/v1/public/login/password"] = `{}`
	if _, err := c.LoginWithPassword(context.Background(), "alice", "pw", ""); err == nil {
		t.Error("login without a session token succeeded")
	}
}

func TestTokenStoringPublicFlows(t *testing.T) {
	rs := newRecordingServer(t)
	c, _ := NewClient(rs.URL)
	ctx := context.Background()

	rs.reply(http.StatusOK, `{"session_token":"wa-session","access_token":"wa-jwt"}`)
	if _, err := c.WebAuthnLoginFinish(ctx, "alice", map[string]interface{}{"id": "x"}); err != nil {
		t.Fatal(err)
	}
	if c.sessionToken != "wa-session" || c.bearerToken != "wa-jwt" {
		t.Errorf("WebAuthnLoginFinish stored %q/%q", c.sessionToken, c.bearerToken)
	}

	rs.reply(http.StatusOK, `{"session_token":"bb-session","access_token":"bb-jwt"}`)
	if _, err := c.RedeemBrowserBootstrap(ctx, "code"); err != nil {
		t.Fatal(err)
	}
	if c.sessionToken != "bb-session" || c.bearerToken != "bb-jwt" {
		t.Errorf("RedeemBrowserBootstrap stored %q/%q", c.sessionToken, c.bearerToken)
	}
	if rs.last.Header.Get("X-Session-Token") != "" {
		t.Error("redeem request carried the previous session token")
	}

	rs.reply(http.StatusOK, `{}`)
	if _, err := c.IssueChallenge(ctx, "alice"); err == nil {
		t.Error("IssueChallenge accepted an empty challenge")
	}
}

func TestGetMachineByName(t *testing.T) {
	ls := newLoginServer(t)
	c, _ := NewClient(ls.URL)
	m, err := c.GetMachineByName(context.Background(), "db-01")
	if err != nil || m.ID != "m2" {
		t.Errorf("GetMachineByName(db-01) = %+v, %v", m, err)
	}
	if _, err := c.GetMachineByName(context.Background(), "ghost"); err == nil || !strings.Contains(err.Error(), `"ghost" not found`) {
		t.Errorf("GetMachineByName(ghost) = %v", err)
	}
}

func TestTransportFailures(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	url := srv.URL
	srv.Close()
	c, _ := NewClient(url, WithSessionToken("s"))
	ctx := context.Background()

	if _, err := c.GetCurrentUser(ctx); err == nil || !strings.Contains(err.Error(), "request failed") {
		t.Errorf("Do = %v", err)
	}
	if _, err := c.GetSessionContent(ctx, "s1"); err == nil || !strings.Contains(err.Error(), "request failed") {
		t.Errorf("doRequestBytes = %v", err)
	}
	if _, err := c.UploadFile(ctx, "m", "/", "", "f", []byte("x")); err == nil || !strings.Contains(err.Error(), "request failed") {
		t.Errorf("doMultipart = %v", err)
	}
}

func TestBinaryAndMultipartEdgeCases(t *testing.T) {
	rs := newRecordingServer(t)
	c, _ := NewClient(rs.URL, WithSessionToken("s"), WithBearerToken("jwt"))
	ctx := context.Background()

	if _, err := c.doRequestBytes(ctx, http.MethodGet, "/x", map[string]string{}, true); err == nil {
		t.Error("doRequestBytes accepted a request body")
	}
	if _, err := c.doRequestBytes(ctx, "BAD METHOD", "/x", nil, true); err == nil || !strings.Contains(err.Error(), "create request") {
		t.Errorf("doRequestBytes bad method = %v", err)
	}
	rs.reply(http.StatusOK, "bytes")
	if _, err := c.doRequestBytes(ctx, http.MethodGet, "/x", nil, true); err != nil {
		t.Fatal(err)
	}
	if rs.last.Header.Get("X-Session-Token") != "s" || rs.last.Header.Get("Authorization") != "Bearer jwt" {
		t.Errorf("binary request headers = %v", rs.last.Header)
	}

	rs.reply(http.StatusRequestEntityTooLarge, `{"message":"too big"}`)
	var apiErr *APIError
	if _, err := c.UploadFile(ctx, "m", "/", "", "f", []byte("x")); !errors.As(err, &apiErr) || apiErr.Message != "too big" {
		t.Errorf("upload message error = %v", err)
	}
	rs.reply(http.StatusBadGateway, `<html>`)
	if _, err := c.UploadFile(ctx, "m", "/", "", "f", nil); !errors.As(err, &apiErr) || apiErr.Body != "<html>" {
		t.Errorf("upload non-JSON error = %v", err)
	}
	rs.reply(http.StatusOK, `not json`)
	if _, err := c.UploadFile(ctx, "m", "/", "", "f", nil); err == nil || !strings.Contains(err.Error(), "parse response") {
		t.Errorf("upload bad JSON = %v", err)
	}
	if err := c.doMultipart(ctx, "BAD METHOD", "/x", nil, "", "", nil, nil); err == nil || !strings.Contains(err.Error(), "create request") {
		t.Errorf("multipart bad method = %v", err)
	}

	var noCtx context.Context
	rs.reply(http.StatusOK, `{"path":"/tmp/f"}`)
	if err := c.doMultipart(noCtx, http.MethodPost, "/files/upload", map[string]string{"machine": "m"}, "", "", nil, nil); err != nil {
		t.Errorf("multipart without file and nil context = %v", err)
	}
	if !strings.HasPrefix(rs.last.Header.Get("Content-Type"), "multipart/form-data") {
		t.Errorf("Content-Type = %q", rs.last.Header.Get("Content-Type"))
	}
}
