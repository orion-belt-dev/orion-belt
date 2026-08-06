package cliflags

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"

	"golang.org/x/crypto/ssh"

	"github.com/zrougamed/orion-belt/pkg/common"
	"github.com/zrougamed/orion-belt/pkg/sdk"
)

// APIEndpointFor resolves the HTTP API base URL from a loaded config, falling
// back to the SSH gateway host on the default API port.
func APIEndpointFor(cfg *common.Config) string {
	if cfg.Server.APIEndpoint != "" {
		return strings.TrimRight(cfg.Server.APIEndpoint, "/")
	}
	return fmt.Sprintf("http://%s:8080", cfg.Server.Host)
}

// SDKClient builds an sdk.Client authenticated with the configured SSH key.
//
// osh/ocp/oadmin subcommands that talk to the REST API go through this so all
// three share one auth path (challenge → signature → API key, with a TOTP
// retry when the organization sets auth.mfa_required).
func (c *Common) SDKClient(cfg *common.Config) (*sdk.Client, error) {
	username, err := c.Username(cfg)
	if err != nil {
		return nil, err
	}

	keyData, err := os.ReadFile(cfg.Auth.KeyFile)
	if err != nil {
		return nil, fmt.Errorf("failed to read SSH key %s: %w", cfg.Auth.KeyFile, err)
	}
	signer, err := ssh.ParsePrivateKey(keyData)
	if err != nil {
		return nil, fmt.Errorf("failed to parse private key %s: %w", cfg.Auth.KeyFile, err)
	}

	client, err := sdk.NewClient(APIEndpointFor(cfg), sdk.WithTimeout(c.Timeout))
	if err != nil {
		return nil, err
	}

	ctx := context.Background()
	_, err = client.LoginWithSSHKey(ctx, username, signer, "")
	if err == nil {
		return client, nil
	}
	if !mfaRequired(err) {
		return nil, fmt.Errorf("authentication failed: %w", err)
	}

	totp, promptErr := PromptSecret("TOTP code: ")
	if promptErr != nil {
		return nil, fmt.Errorf("mfa required: %w", promptErr)
	}
	if totp == "" {
		return nil, fmt.Errorf("mfa code required")
	}
	if _, err := client.LoginWithSSHKey(ctx, username, signer, totp); err != nil {
		return nil, fmt.Errorf("authentication failed: %w", err)
	}
	return client, nil
}

// APIClient loads the config and returns an authenticated SDK client, the
// single call most subcommands need.
func (c *Common) APIClient() (*sdk.Client, error) {
	cfg, err := c.LoadConfig()
	if err != nil {
		return nil, err
	}
	return c.SDKClient(cfg)
}

// mfaRequired reports whether a login error is the server asking for a TOTP
// code rather than a genuine failure. The server answers 401 with
// {"error":"mfa code required","mfa_required":true}; APIError.Error() only
// renders the "error" field, so the flag has to be read off the raw body.
func mfaRequired(err error) bool {
	if err == nil {
		return false
	}
	var apiErr *sdk.APIError
	if !errors.As(err, &apiErr) {
		return false
	}
	return strings.Contains(apiErr.Body, `"mfa_required":true`) ||
		strings.Contains(apiErr.Body, `"mfa_required": true`)
}
