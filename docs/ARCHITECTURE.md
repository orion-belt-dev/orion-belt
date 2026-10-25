# Orion Belt - Architecture Overview

## System Architecture

```
┌─────────────────────────────────────────────────────────────────────────┐
│                           CLIENT LAYER                                  │
├─────────────────────────────────────────────────────────────────────────┤
│  ┌──────────┐  ┌──────────┐  ┌──────────────┐  ┌─────────────────────┐  │
│  │ osh/ocp  │  │  oadmin  │  │ OpenSSH ssh  │  │ Web console (/ui)   │  │
│  │   CLI    │  │   CLI    │  │ user+host@gw │  │ terminal + files    │  │
│  └────┬─────┘  └────┬─────┘  └──────┬───────┘  └──────────┬──────────┘  │
│       └─────────────┴───────┬───────┘                     │             │
│                             │ SSH :2222                   │ HTTP :8080  │
└─────────────────────────────┼─────────────────────────────┼─────────────┘
                              ▼                             ▼
┌────────────────────────────────────────────────────────────────────────┐
│                        GATEWAY SERVER                                  │
│  ┌────────────────────────────┐   ┌─────────────────────────────────┐  │
│  │ SSH Proxy (2222)           │   │ REST API + /ui + /metrics       │  │
│  │ • Pubkey / FIDO sk-*       │   │ • JWT / API key / session       │  │
│  │ • user+machine routing     │   │ • WebAuthn / TOTP MFA           │  │
│  │ • Session + PTY forward    │   │ • Terminal WS / file browser    │  │
│  └───────────┬────────────────┘   └─────────────────────────────────┘  │
│              │                                                         │
│  ┌───────────▼────────┐  ┌──────────────┐  ┌────────────────────────┐  │
│  │ Auth / Authz       │  │ Recorder     │  │ Plugins (Slack/email/  │  │
│  │ ReBAC + OpenFGA    │  │ AES optional │  │ webhook/audit)         │  │
│  │ Roles: admin/…     │  │ + retention  │  └────────────────────────┘  │
│  └───────────┬────────┘  └──────────────┘                              │
│              ▼                                                         │
│  ┌──────────────────────────────────────────────────────────┐          │
│  │ PostgreSQL: users, keys, webauthn, machines, sessions,   │          │
│  │ permissions, access_requests, audit_logs, api_keys, …    │          │
│  └──────────────────────────────────────────────────────────┘          │
└───────────────────────────────────┬────────────────────────────────────┘
                                    │ reverse SSH (agents)
              ┌─────────────────────┼─────────────────────┐
              ▼                     ▼                     ▼
        ┌───────────┐         ┌───────────┐         ┌───────────┐
        │ Agent +   │         │ Agent +   │         │ Agent +   │
        │ local OS  │         │ local OS  │         │ local OS  │
        └───────────┘         └───────────┘         └───────────┘
```

## How It Works

### 1. Agent Registration & Connection
Agents establish persistent SSH connections to the gateway. With **SSH CA** enabled, new agents authenticate with Host-CA-signed certificates (no synthetic user row); otherwise they use SSH keys tied to a synthetic agent user. The gateway keeps reverse tunnels so targets need no inbound firewall rules. See [SSH_CA.md](SSH_CA.md).

### 2. Client Authentication
Clients authenticate with SSH public keys (including FIDO `sk-*` keys) or short-lived **User certificates** from the internal SSH CA. Gateway identity may be encoded as `alice+web-01` for OpenSSH agentless interactive sessions. HTTP/API clients use API keys, session tokens, JWT, challenge-response key login, and optional TOTP/WebAuthn. When CA is enabled, the gateway presents a Host certificate for cert-aware client verification.

### 3. Connection Flow
1. Authenticate gateway user  
2. Check ReBAC (and OpenFGA if enabled)  
3. Open a **session** on the connected agent (`shell` / `exec`) — not `direct-tcpip` to remote sshd  
4. Record I/O; enforce remote-user ACL  

RDP and VNC take a different path: the client opens `direct-tcpip` to the gateway (port 3389 or 5900, host = machine name), the gateway checks an `rdp`/`vnc` grant and relays the stream over an `orion-tcpip` channel to the agent, which connects to the local service. See [RDP_VNC.md](RDP_VNC.md).

### 4. Session Recording
PTY **output** is recorded as a timed cast (asciinema-compatible v2, `.cast` under `recording.storage_path`). Recordings may be AES-GCM encrypted at rest, with retention cleanup for `.cast` / legacy `.txt` / `.rec` files. The web console replays casts in xterm (play/pause/seek).

### 5. Access Request Workflow
Users request temporary access; admins/operators approve via API, `oadmin`, or `/ui`. Grants are time-limited.

### 6. Web Console
Embedded SPA at `/ui` (roles: admin, operator, auditor, user) with approvals, live terminal (WebSocket, recorded as `source=web`), file browser, timed session playback, **Add agent** install scripts, audit trail, user/machine management, permission editor (bulk grant/revoke, effective access), capability catalog, compliance report export, and build version display. See [SRS-UI.md](SRS-UI.md).

### 7. Kubernetes
An agent inside the cluster dials out like a host agent and announces Kubernetes support. An approved `k8s` grant becomes a RoleBinding or ClusterRoleBinding for `orion:<username>`; the gateway reconciles bindings against active grants (on change, on agent connect, every 30s) and the agent deletes expired bindings on its own. `osh kube` sends kubectl traffic to the gateway (`orion-kube@orion-belt.dev` channel), which relays it to the agent over `orion-k8s`; the agent forwards it to the API server with impersonation. See [KUBERNETES.md](KUBERNETES.md).

### 8. Tunnel resilience
Gateway, agents and clients exchange `keepalive@openssh.com` requests. A dropped agent reconnects with jittered backoff and replaces its old connection; `osh` keeps the local RDP/VNC/Kubernetes listener open across drops and redials the gateway.

### 9. Agent management
Admins send `orion:status`, `orion:reload`, `orion:drain`/`undrain` and `orion:update` to an agent over its existing connection (`oadmin agents …` or the admin API). See [AGENT_MANAGEMENT.md](AGENT_MANAGEMENT.md).

### 10. Access decisions and risk
`/access/explain` reports why a user can or can't reach a machine. Access requests carry risk signals, and a risk policy can auto-approve or escalate them; anomalies are flagged, never blocked. See [RISK.md](RISK.md) and [COMPLIANCE_REPORTS.md](COMPLIANCE_REPORTS.md).

## Key Features

**Security**: SSH encryption, pubkey/FIDO auth, MFA, ReBAC/OpenFGA, session recording, audit logs, risk policy.  
**Scalability**: Reverse-tunnel agents; single gateway for many machines.  
**Auditability**: Recordings, audit API, access-request and permission history, SOC2/ISO 27001/PCI reports.  
**Flexibility**: Temporary grants, tags, plugins, OpenSSH and CLI clients; SSH, SCP, RDP, VNC and Kubernetes.

## Data Flow Example

```
OpenSSH: ssh alice+web-01@gateway
    → Gateway pubkey (+ optional FIDO touch on client)
    → Parse target web-01 / remote user
    → Permission check
    → Agent session (PTY + shell)
    → I/O recorded
```

## Technology Stack

- **Language**: Go  
- **Database**: PostgreSQL  
- **Protocol**: SSH (`golang.org/x/crypto/ssh`), HTTP/WS (Gin); RDP/VNC relayed as raw TCP; Kubernetes REST without client-go  
- **MFA**: TOTP (`pquerna/otp`), WebAuthn (`go-webauthn`)  
- **Architecture**: Bastion with reverse tunnels  
- **Deployment**: Docker Compose, native packages (deb/rpm/apk, GPG-signed repos), Alpine, or any Unix-like host
- **API docs**: OpenAPI 3.0 at `docs/openapi/openapi.yaml` / `GET /api/v1/openapi.yaml`

## Documentation index

| Doc | Status |
|-----|--------|
| [README.md](../README.md) | Current overview |
| [ARCHITECTURE.md](ARCHITECTURE.md) | Current |
| [SRS-UI.md](SRS-UI.md) | Web console SRS (as implemented) |
| [openapi/openapi.yaml](openapi/openapi.yaml) | OpenAPI 3.0 (also `GET /api/v1/openapi.yaml`) |
| [API/README.md](API/README.md) | Swagger/Postman how-to |
| [SSH_CA.md](SSH_CA.md) | Internal SSH CA (user/host certs, renewal, revoke) |
| [PACKAGING.md](PACKAGING.md) | deb/rpm/apk + GPG-signed repos |
| [CLIENT-CLI.md](CLIENT-CLI.md) | osh / ocp / oadmin flags |
| [RDP_VNC.md](RDP_VNC.md) | RDP and VNC through the agent tunnel |
| [KUBERNETES.md](KUBERNETES.md) | Just-in-time Kubernetes RoleBindings |
| [AGENT_MANAGEMENT.md](AGENT_MANAGEMENT.md) | Reload, drain, update agents |
| [COMPLIANCE_REPORTS.md](COMPLIANCE_REPORTS.md) | SOC2 / ISO 27001 / PCI reports |
| [RISK.md](RISK.md) | Risk signals, risk policy, anomalies |
| [ERROR_HANDLING.md](ERROR_HANDLING.md) | `pkg/apperr` and API error responses |
| [ROADMAP.md](ROADMAP.md) | Current |
| [V1_RELEASE_CRITERIA.md](V1_RELEASE_CRITERIA.md) | Before tagging v1.0 |
| [API_STABILITY.md](API_STABILITY.md) | `/api/v1` compatibility |
| [RELEASE_SMOKE.md](RELEASE_SMOKE.md) | Quick pre-release checks |
| [OBSERVABILITY.md](OBSERVABILITY.md) | Logs, metrics and tracing |
| [DEPLOYMENT_HARDENING.md](DEPLOYMENT_HARDENING.md) | Hardening checklist |
| [SETUP.md](SETUP.md) | First-run |
| [openssh-clients.md](openssh-clients.md) | Current |
| [openfga-model.fga](openfga-model.fga) | Current |
| [PLUGIN_DEVELOPMENT.md](PLUGIN_DEVELOPMENT.md) | Plugin API |
| [SERVER-CLI.md](SERVER-CLI.md) | Server CLI |
| [CONTRIBUTING.md](CONTRIBUTING.md) | Current |
| [E2E_TEST_PLAN.md](E2E_TEST_PLAN.md) | QEMU lab QA |

**Still open:** OIDC / SAML / LDAP, secrets vault, database gateway, HA, OPA. See [ROADMAP.md](ROADMAP.md#open-work--2026-10-25).
