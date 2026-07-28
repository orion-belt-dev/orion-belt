# Go SDK

Orion Belt ships a reusable Go SDK at import path:

github.com/zrougamed/orion-belt/pkg/sdk

The SDK is intended for automation and integrations that need to call the
Orion Belt HTTP API from Go.

## Create a client

```go
package main

import (
	"context"
	"errors"
	"log"
	"os"

	"github.com/zrougamed/orion-belt/pkg/sdk"
	"golang.org/x/crypto/ssh"
)

func main() {
	client, err := sdk.NewClient("https://pam.example.com", sdk.WithAPIKey("obk_xxx"))
	if err != nil {
		log.Fatal(err)
	}

	machines, err := client.ListMachines(context.Background())
	if err != nil {
		log.Fatal(err)
	}

	log.Printf("machines: %d", len(machines))
}
```

## Authentication options

- `sdk.WithAPIKey("...")` for API key auth (`X-API-Key`)
- `sdk.WithSessionToken("...")` for session auth (`X-Session-Token`)
- `sdk.WithBearerToken("...")` for JWT auth (`Authorization: Bearer ...`)

You can also update credentials after creation:

```go
client.SetAPIKey("obk_new")
client.SetSessionToken("session_token")
client.SetBearerToken("jwt_token")
```

## Password login flow

Use password+TOTP login for integrations that cannot sign SSH challenges:

```go
resp, err := client.LoginWithPassword(ctx, "admin", "password", "123456")
if err != nil {
	return err
}

_ = resp.SessionToken // client stores it automatically
```

## SSH challenge login flow

If your integration has access to an SSH private key, use challenge login:

```go
key, err := os.ReadFile("/path/to/id_ed25519")
if err != nil {
	return err
}
signer, err := ssh.ParsePrivateKey(key)
if err != nil {
	return err
}

loginResp, err := client.LoginWithSSHKey(ctx, "admin", signer, "")
if err != nil {
	return err
}

_ = loginResp.APIKey // client stores it automatically
```

## Included endpoint helpers

- Machines: `ListMachines`, `GetMachineByName`
- Access requests: `CreateAccessRequest`, `GetAccessRequest`, `ListPendingAccessRequests`, `ApproveAccessRequest`, `RejectAccessRequest`
- SSH CA and certs: `GetTrustedCA`, `IssueUserCert`, `ExportCA`, `ListSSHCertificates`, `RevokeSSHCertificate`
- Browser bootstrap: `RequestBrowserBootstrap`
- Auth/session: `GetCurrentUser`, `Logout`, `LoginWithPassword`, `LoginWithSSHKey`
- API keys: `CreateAPIKey`, `ListAPIKeys`, `RevokeAPIKey`, `DeleteAPIKey`
- Users: `ListUsers`, `GetUser`, `CreateUser`, `UpdateUser`, `DeleteUser`
- Machines/permissions (admin + protected lookups):
	`GetMachine`, `CreateMachine`, `UpdateMachine`, `DeleteMachine`,
	`GetUserPermissions`, `GetMachinePermissions`, `ListAllPermissions`,
	`GrantPermission`, `UpdatePermission`, `RevokePermission`
- Sessions/audit/reports/dashboard:
	`ListSessions`, `ListActiveSessions`, `GetSession`, `GetSessionContent`,
	`ListAuditLogs`, `ExportReport`, `GetUsageDashboard`
- Notifications/setup:
	`ListNotifications`, `GetUnreadNotificationsCount`, `MarkNotificationRead`,
	`MarkAllNotificationsRead`, `GetNotificationPrefs`, `UpdateNotificationPrefs`,
	`GetSetupStatus`
- Plugins/agents (admin):
	`ListPlugins`, `UpdatePluginConfig`, `EnablePlugin`, `DisablePlugin`,
	`ListConnectedAgents`, `SendAgentCommand`, `DisconnectAgent`,
	`GenerateAgentInstallScript`
- Security UX endpoints:
	`MFAEnroll`, `MFAConfirm`, `MFADisable`, `MFAStatus`,
	`SetPassword`, `ClearPassword`,
	`WebAuthnRegisterBegin`, `WebAuthnRegisterFinish`, `WebAuthnCredentials`,
	`WebAuthnDeleteCredential`, `WebAuthnLoginBegin`, `WebAuthnLoginFinish`,
	`ListSSHKeys`, `AddSSHKey`, `DeleteSSHKey`
- Public auth/registration:
	`IssueChallenge`, `LoginWithSSHSession`, `LoginWithSSHJWT`,
	`RedeemBrowserBootstrap`, `RegisterAgent`, `RegisterClient`
- File browser operations:
	`ListFiles`, `DownloadFile`, `UploadFile`, `MakeDir`, `DeleteFile`

For any endpoint not yet wrapped, use low-level helpers:

- `Do(ctx, method, path, body, out)` for authenticated calls
- `DoPublic(ctx, method, path, body, out)` for public calls

Paths are automatically prefixed with `/api/v1` unless you provide an absolute
URL or a path already starting with `/api/`.

## Error handling

Non-2xx responses return `*sdk.APIError`:

```go
var apiErr *sdk.APIError
if errors.As(err, &apiErr) {
	log.Printf("status=%d message=%s", apiErr.StatusCode, apiErr.Message)
}
```
