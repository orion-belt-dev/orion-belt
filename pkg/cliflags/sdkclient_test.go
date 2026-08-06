package cliflags

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"

	"github.com/orion-belt-dev/orion-belt/pkg/common"
	"github.com/orion-belt-dev/orion-belt/pkg/sdk"
)

func TestAPIEndpointFor(t *testing.T) {
	cfg := &common.Config{}
	cfg.Server.Host = "gateway.example.com"
	if got := APIEndpointFor(cfg); got != "http://gateway.example.com:8080" {
		t.Errorf("without an explicit endpoint, want the gateway host on 8080, got %q", got)
	}

	cfg.Server.APIEndpoint = "https://pam.example.com/"
	if got := APIEndpointFor(cfg); got != "https://pam.example.com" {
		t.Errorf("the trailing slash should be trimmed, got %q", got)
	}
}

func TestMFARequiredReadsTheRawBody(t *testing.T) {
	// The server sends {"error":"mfa code required","mfa_required":true} and
	// APIError.Error() renders only the "error" field, so the flag has to come
	// off the body.
	needsMFA := &sdk.APIError{
		StatusCode: http.StatusUnauthorized,
		Message:    "mfa code required",
		Body:       `{"error":"mfa code required","mfa_required":true}`,
	}
	if !mfaRequired(needsMFA) {
		t.Error("an mfa_required response should be recognized")
	}

	wrongKey := &sdk.APIError{
		StatusCode: http.StatusUnauthorized,
		Message:    "invalid credentials",
		Body:       `{"error":"invalid credentials"}`,
	}
	if mfaRequired(wrongKey) {
		t.Error("a plain auth failure is not an MFA prompt")
	}
	if mfaRequired(nil) {
		t.Error("nil is not an MFA prompt")
	}
}

// writeTestKey writes a throwaway ed25519 private key and returns its path.
func writeTestKey(t *testing.T, dir string) string {
	t.Helper()

	_, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	block, err := ssh.MarshalPrivateKey(private, "")
	if err != nil {
		t.Fatalf("marshal key: %v", err)
	}

	path := filepath.Join(dir, "id_ed25519")
	if err := os.WriteFile(path, pem.EncodeToMemory(block), 0o600); err != nil {
		t.Fatalf("write key: %v", err)
	}
	return path
}

func TestSDKClientAuthenticatesWithTheConfiguredKey(t *testing.T) {
	var signedChallenge string

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/public/auth/challenge":
			writeJSON(t, w, map[string]string{"challenge": "test-challenge"})
		case "/api/v1/public/login/key":
			var req map[string]any
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				t.Errorf("decode login request: %v", err)
			}
			signedChallenge, _ = req["challenge"].(string)
			if req["signature"] == "" {
				t.Error("login should carry a signature over the challenge")
			}
			writeJSON(t, w, map[string]any{"api_key": "test-api-key"})
		default:
			t.Errorf("unexpected request to %s", r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()

	cfg := &common.Config{}
	cfg.Server.APIEndpoint = server.URL
	cfg.Auth.KeyFile = writeTestKey(t, t.TempDir())
	cfg.Auth.User = "alice"

	flags := &Common{Timeout: 5 * time.Second}
	client, err := flags.SDKClient(cfg)
	if err != nil {
		t.Fatalf("SDKClient: %v", err)
	}
	if client == nil {
		t.Fatal("SDKClient returned no client")
	}
	if signedChallenge != "test-challenge" {
		t.Errorf("client signed %q, want the server's challenge", signedChallenge)
	}
}

func TestSDKClientReportsAMissingKeyFile(t *testing.T) {
	cfg := &common.Config{}
	cfg.Server.APIEndpoint = "http://127.0.0.1:1"
	cfg.Auth.KeyFile = filepath.Join(t.TempDir(), "absent")
	cfg.Auth.User = "alice"

	flags := &Common{Timeout: time.Second}
	if _, err := flags.SDKClient(cfg); err == nil {
		t.Fatal("expected an error when the key file does not exist")
	}
}

func TestSDKClientRequiresAUsername(t *testing.T) {
	cfg := &common.Config{}
	cfg.Server.APIEndpoint = "http://127.0.0.1:1"

	t.Setenv("ORION_USER", "")
	t.Setenv("USER", "")

	flags := &Common{Timeout: time.Second}
	if _, err := flags.SDKClient(cfg); err == nil {
		t.Fatal("expected an error when no username can be resolved")
	}
}

func writeJSON(t *testing.T, w http.ResponseWriter, payload any) {
	t.Helper()
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(payload); err != nil {
		t.Errorf("encode response: %v", err)
	}
}
