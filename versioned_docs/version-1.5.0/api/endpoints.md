---
sidebar_position: 22
title: API Endpoints
---

# API Endpoints

Every path below is relative to the server's HTTP address, for example
`https://orion.example.com`. The full request and response schemas are in the
OpenAPI document the server serves at `/api/v1/openapi.yaml` (and
`/api/v1/openapi.json`).

| Access | Meaning |
| --- | --- |
| **Public** | No credentials; rate-limited per IP |
| **User** | Any signed-in user: `Authorization: Bearer <API key or JWT>`, or the console session cookie |
| **Admin** | Role `admin` or `operator` |

Errors use one body format; see [Errors](./#errors).

## Server

| Method | Path | Access | Description |
| --- | --- | --- | --- |
| GET | `/health` | Public | Liveness check |
| GET | `/api/v1/version` | Public | Version, commit and build date |
| GET | `/api/v1/gateway-info` | Public | Public URL and SSH address agents and clients use |
| GET | `/api/v1/openapi.yaml`, `/api/v1/openapi.json` | Public | OpenAPI document |
| GET | `/metrics` | Public | Prometheus metrics, when enabled |

## Sign-in and registration

| Method | Path | Access | Description |
| --- | --- | --- | --- |
| POST | `/api/v1/public/auth/challenge` | Public | Challenge to sign with your SSH key |
| POST | `/api/v1/public/login/key` | Public | Sign in with a signed challenge; returns an API key |
| POST | `/api/v1/public/login/token` | Public | Sign in and get a JWT |
| POST | `/api/v1/public/login/password` | Public | Sign in with password (and TOTP when enrolled) |
| POST | `/api/v1/public/login` | Public | Sign in with your SSH key; returns a session and an optional JWT |
| POST | `/api/v1/public/webauthn/login/begin`, `…/finish` | Public | Sign in with a security key |
| POST | `/api/v1/public/auth/browser-bootstrap/redeem` | Public | Redeem a one-time console sign-in code (`osh login`) |
| POST | `/api/v1/public/register/agent` | Admin | Register an agent's public key (public path kept for old scripts; needs admin or operator credentials) |
| POST | `/api/v1/public/register/client` | Admin | Create a user with a public key; anonymous only for the very first account, which becomes admin |
| POST | `/api/v1/logout` | User | End the current session |
| GET | `/api/v1/auth/me` | User | The signed-in user |
| POST | `/api/v1/auth/browser-bootstrap` | User | Issue a one-time console sign-in code |
| POST, DELETE | `/api/v1/auth/password` | User | Set or remove your password |

## Your credentials

| Method | Path | Access | Description |
| --- | --- | --- | --- |
| GET, POST | `/api/v1/api-keys` | User | List or create API keys |
| POST | `/api/v1/api-keys/{id}/revoke` | User | Revoke an API key |
| DELETE | `/api/v1/api-keys/{id}` | User | Delete an API key |
| GET, POST | `/api/v1/ssh-keys` | User | List or add SSH public keys |
| DELETE | `/api/v1/ssh-keys/{id}` | User | Remove an SSH key |
| GET | `/api/v1/mfa/status` | User | TOTP enrollment status |
| POST | `/api/v1/mfa/enroll`, `/api/v1/mfa/confirm`, `/api/v1/mfa/disable` | User | Enroll, confirm or disable TOTP |
| POST | `/api/v1/webauthn/register/begin`, `…/finish` | User | Register a security key |
| GET | `/api/v1/webauthn/credentials` | User | List security keys |
| DELETE | `/api/v1/webauthn/credentials/{id}` | User | Remove a security key |
| GET | `/api/v1/ssh-cert/ca` | User | The user CA's public key |
| POST | `/api/v1/ssh-cert` | User | Issue a short-lived SSH user certificate |

## Users and machines

| Method | Path | Access | Description |
| --- | --- | --- | --- |
| GET | `/api/v1/users`, `/api/v1/users/{id}` | User | List users, or get one |
| POST | `/api/v1/admin/users` | Admin | Create a user |
| PUT, DELETE | `/api/v1/admin/users/{id}` | Admin | Update or delete a user |
| GET | `/api/v1/machines`, `/api/v1/machines/{id}` | User | List machines, or get one |
| POST | `/api/v1/admin/machines` | Admin | Create a machine |
| PUT, DELETE | `/api/v1/admin/machines/{id}` | Admin | Update or delete a machine |
| GET, POST | `/api/v1/admin/machines/{id}/kubernetes/reconcile` | Admin | Report (GET) or fix (POST) Kubernetes binding drift |

## Agents

| Method | Path | Access | Description |
| --- | --- | --- | --- |
| GET | `/api/v1/admin/agents/connected` | Admin | Agents connected now |
| POST | `/api/v1/admin/agents/install-script` | Admin | Register a machine and generate its install script |
| POST | `/api/v1/admin/agents/{machine_id}/command` | Admin | Send `orion:ping`, `orion:health`, `orion:status` or `orion:info`; after 1.4.0 also `orion:reload`, `orion:drain`, `orion:undrain` and `orion:update` (see [Agent remote management](../guides/agent-management)) |
| POST | `/api/v1/admin/agents/{machine_id}/disconnect` | Admin | Drop an agent's connection |

## Permissions and access requests

| Method | Path | Access | Description |
| --- | --- | --- | --- |
| GET | `/api/v1/permissions/user/{id}` | User | A user's grants |
| GET | `/api/v1/permissions/machine/{id}` | User | Grants on a machine |
| GET | `/api/v1/admin/permissions` | Admin | List grants; after 1.4.0 filtered by `q`, `access_type`, `user_id`, `machine_id` and paged, with `X-Total-Count` |
| POST | `/api/v1/admin/permissions` | Admin | Grant access |
| PATCH, DELETE | `/api/v1/admin/permissions/{id}` | Admin | Change or revoke a grant |
| POST | `/api/v1/admin/permissions/bulk` | Admin | Grant one access type to every user × machine pair, up to 500 (after 1.4.0) |
| POST | `/api/v1/admin/permissions/bulk-revoke` | Admin | Revoke several grants, up to 500 (after 1.4.0) |
| GET | `/api/v1/admin/permissions/effective` | Admin | Effective access for a user or a machine (after 1.4.0) |
| GET | `/api/v1/capabilities` | User | What you have, have asked for, or can request |
| GET | `/api/v1/access/explain` | User | Why a user can or can't reach a machine |
| GET, POST | `/api/v1/access-requests` | User | List or create access requests |
| GET | `/api/v1/access-requests/{id}` | User | One request, with its risk signals |
| GET | `/api/v1/admin/access-requests/pending` | Admin | Requests waiting for a decision |
| POST | `/api/v1/admin/access-requests/{id}/approve`, `…/reject` | Admin | Approve or reject a request |
| GET, PUT | `/api/v1/admin/risk-policy` | Admin | Risk-based auto-approve and escalate rules |

## Sessions, terminal and files

| Method | Path | Access | Description |
| --- | --- | --- | --- |
| GET | `/api/v1/sessions`, `/api/v1/sessions/active` | User | Session history, or sessions running now |
| GET | `/api/v1/sessions/{id}` | User | One session |
| GET | `/api/v1/sessions/{id}/content` | User | The recording (`.cast`) |
| GET | `/api/v1/sessions/{id}/watch` | User | Watch a running session (WebSocket) |
| GET | `/api/v1/terminal/ws` | User | Web terminal (WebSocket) |
| GET | `/api/v1/files/list`, `/api/v1/files/download` | User | Browse or download files on a machine |
| POST | `/api/v1/files/upload`, `/api/v1/files/mkdir` | User | Upload a file or create a directory |
| DELETE | `/api/v1/files` | User | Delete a file |

## Audit, reports and analytics

| Method | Path | Access | Description |
| --- | --- | --- | --- |
| GET | `/api/v1/audit-logs` | User | Audit log |
| GET | `/api/v1/reports/{name}/export` | User | Export a [compliance report](../guides/compliance-reports) as CSV, PDF or JSON |
| GET | `/api/v1/dashboard/usage` | User | Usage dashboard data |
| GET | `/api/v1/anomalies` | User | Flagged anomalies |
| GET | `/api/v1/setup/status` | User | First-run checklist status |

## Notifications

| Method | Path | Access | Description |
| --- | --- | --- | --- |
| GET | `/api/v1/notifications`, `/api/v1/notifications/unread-count` | User | In-app notifications |
| POST | `/api/v1/notifications/{id}/read`, `/api/v1/notifications/read-all` | User | Mark notifications read |
| GET, PUT | `/api/v1/notifications/prefs` | User | Your notification preferences |
| GET, PUT | `/api/v1/admin/notifications/policy` | Admin | Which events users can switch off |
| GET | `/api/v1/admin/notifications/templates` | Admin | Notification templates |
| PUT, DELETE | `/api/v1/admin/notifications/templates/{event}` | Admin | Set or reset a template |

## Plugins and SSH CA

| Method | Path | Access | Description |
| --- | --- | --- | --- |
| GET | `/api/v1/admin/plugins` | Admin | Plugins and their settings |
| PUT | `/api/v1/admin/plugins/{name}/config` | Admin | Configure a plugin |
| POST | `/api/v1/admin/plugins/{name}/enable`, `…/disable` | Admin | Turn a plugin on or off |
| ANY | `/api/v1/public/plugins/{name}/…` | Public | Plugin webhooks (ChatOps buttons); each plugin verifies its own signatures |
| GET | `/api/v1/admin/ca/export` | Admin | Export the CA public keys |
| GET | `/api/v1/admin/ssh-certificates` | Admin | Issued SSH certificates |
| POST | `/api/v1/admin/ssh-certificates/{serial}/revoke` | Admin | Revoke a certificate |
