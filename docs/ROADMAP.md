# Orion Belt — Development Roadmap

## Current Status

**Version:** v1.0.0
**Status:** **v1.0.0** — stable SSH PAM release. Next up is OIDC / HA / multi-protocol / SDKs / compliance (see below).

## Pending / Next Up — 2026-07-14

### v1.0.0 (done)

Shipped as a stability cut of what’s already on `master` (CA, MFA, JIT, ReBAC, recording, live watch, packages) — not a new IdP or protocol. Notes: [V1_RELEASE_CRITERIA.md](V1_RELEASE_CRITERIA.md), [API_STABILITY.md](API_STABILITY.md), [RELEASE_SMOKE.md](RELEASE_SMOKE.md) (`make release-smoke`).

### Still open

- [ ] HashiCorp Vault
- [ ] Fancier agent remote management (reload, drain, update)
- [ ] Better unit coverage / integration suites
- [ ] OpenTelemetry tracing (logs + metrics are in)

### After v1.0.0

- OIDC (Okta / Entra / Keycloak / Google / friends) + group → role/grant mapping; keep a local break-glass admin
- SAML / LDAP
- HA / DB replicas
- RDP / VNC / Kubernetes / DB proxies
- Client SDKs
- Compliance reporting packs
- SIEM / ticketing, RBAC-on-ReBAC, command filtering

Permissions editor, recording compression, live watch, notification prefs, and JIT polish landed on the way here (historically called v0.9 work).

## Audit Findings — 2026-07-13

A full pass over the backend (`pkg/`, `cmd/`, `plugins/`), docs (`docs/`, `README.md`, OpenAPI spec), and web console (`web/ui/src`) turned up concrete, specific gaps — listed here rather than folded silently into the sections below so they don't get lost among the aspirational Phase 2-4 items. Two were security-critical and fixed same-day; the rest are queued.

All items below were found and fixed same-day (backend, docs, and web-console work landed in parallel).

### Security / correctness

- [x] **Agent shell spawn no longer shells out to `su -`.** `pkg/agent/agent.go` now resolves the target user from the local user database and drops privileges directly via `syscall.Credential` (setuid/setgid), resolving the user's real login shell from `/etc/passwd` instead of assuming `/bin/bash` exists. Matches how OpenSSH spawns sessions; fixes hard failures on minimal distro images (e.g. `agent-opensuse`) where `su`'s PAM/setuid path was unreliable.
- [x] **CRITICAL — command injection in the web file browser, fixed.** `pkg/api/terminal.go`'s `filesList`/`filesDownload`/`filesUpload`/`filesMkdir`/`filesDelete` built shell command strings with Go's `%q` verb, which escapes Go-string syntax only — not shell metacharacters. `$(...)` command substitution still expanded inside the resulting double-quoted shell token, so a `path` like `/tmp/$(id>/tmp/pwned)` executed arbitrary commands on the target agent. All five handlers now use the existing `shellQuote()` POSIX single-quote escaper. Regression-tested in `pkg/api/terminal_test.go`.
- [x] **`exec` requests now forward `remote_user` to the agent.** File ops and CLI `exec` previously always ran as the agent's own uid (root) regardless of the selected remote user — only the interactive `shell` request carried a `User` field. Added `User` to the exec payload on both the web-terminal-bridge path (`pkg/api/terminal.go` `execOnMachine`) and the CLI/gateway path (`pkg/server/server.go`); `pkg/agent/agent.go`'s `executeCommand` now impersonates that user via the same setuid/setgid machinery `buildShellCommand` uses for interactive shells (factored into shared `resolveUnixIdentity()`/`credentialForIdentity()` helpers), falling back to the agent's own identity when no user is specified (admin control commands, heartbeat).
- [x] **Plugin hooks now have panic recovery and a bounded timeout.** `pkg/plugin/plugin.go`'s `TriggerHook` runs each plugin via `recover()`-guarded `callHook`, raced against a timeout (default 5s) so a panicking or hanging plugin (e.g. a webhook with no HTTP timeout) can no longer crash the server or stall every login/session. Covered in `pkg/plugin/plugin_test.go`.
- [x] **Plugin config wiring no longer picks a plugin at random.** `pkg/plugin/plugin_loader.go`'s `LoadPlugin` now returns the just-loaded plugin's real name instead of `LoadPluginWithConfig` re-deriving "the" plugin via undefined map-iteration order over `loadedLibs`. *(Superseded 2026-07-14 — `plugin_loader.go` and dynamic `.so` loading were removed entirely; see "Compiled-in plugin platform" below.)*
- [x] **Expired HTTP sessions are now cleaned up.** `pkg/server/server.go` has a new `runSessionCleanupLoop` (hourly ticker, same shutdown-channel pattern as the recording retention loop) calling the previously-unwired `AuthService.CleanupExpiredSessions`.

### Documentation

- [x] **`docs/openapi/openapi.yaml`** now documents `POST /api/v1/admin/agents/{machine_id}/disconnect`, matching the sibling `/admin/agents/*` entries.
- [x] **`docs/SETUP.md`** now points readers at `config/server.example.yaml` right after the minimal-config step, with one-line explanations of the WebAuthn/OpenFGA/MFA-enforcement/rate-limiting/recording-encryption knobs that were previously undiscoverable.
- [x] **`docs/SREVER-CLI.md` filename typo, fixed** — renamed to `docs/SERVER-CLI.md`; fixed the inbound link and typo callout in `ARCHITECTURE.md`.
- [x] **`docs/SRS-UI.md`'s role/nav table** now includes the Permissions page (admin/operator only, confirmed against `web/ui/src/lib/nav.ts`).

### Web console

- [x] **`PermissionsPage.tsx` `revoke()` now requires confirmation**, matching every other destructive action in the console.
- [x] **`PermissionsPage.tsx` migrated to the shared `DataTable`/`useTableState` pattern**, with a new "view by user / by machine" toggle wired to the existing `GET /permissions/machine/:id` endpoint — the concrete substance behind the old "richer permission editor" line.
- [x] **API key management UI added** — new "API keys" tab in `SecurityPage.tsx` alongside MFA/WebAuthn/SSH keys: create (with optional expiry), one-time copyable reveal of the raw key on creation (mirrors the MFA-enrollment "show secret once" UX), and separate soft-Revoke vs. permanent-Delete actions matching the backend's actual semantics.
- [x] **`SetupPage.tsx` onboarding fixed — and a larger bug found in the process.** Step 5 ("Harden auth") no longer hardcodes `done: false` (now derived from the signed-in user's MFA/WebAuthn enrollment status). While fixing it, discovered the page was reading fields (`has_admin`, `agents_connected`, `machines`, `ready`) that don't exist in the real `/setup/status` response shape (`complete`, `steps.*`, `counts.*`) — so steps 2, 3, 4, and 6 (not just 5) were silently stuck on "todo" regardless of actual state. Fixed the type and all step/stat derivations to match the real backend response. Also replaced the dead-ternary `BadgeDone` component with the existing shared `Badge` component instead of inventing new CSS.

---

## What Orion Belt Is

Orion Belt is a lightweight, self-hosted Privileged Access Management (PAM) system built in Go. It implements a bastion host pattern using *reverse SSH tunnels*, fine-grained access control (ReBAC), session recording, and an approval-based workflow for temporary access. The goal is secure, auditable access to infrastructure with no open inbound ports.

---

## Done

### v0.1 — Core System

#### Gateway Server

* **SSH Proxy (Port 2222)**
  - [x] Full SSH implementation using **golang.org/x/crypto/ssh**
  - [x] Public-key auth backed by PostgreSQL
  - [x] Concurrent multi-session handling
  - [x] Session routing to agents (exec/shell; OpenSSH `user+machine` form)
  - [x] Full session lifecycle support

* **REST API (Port 8080)**
  - [x] Endpoints for user/machine/permission management
  - [x] Access request endpoints
  - [x] Session listing and audit log retrieval
  - [x] Agent registration
  - [x] Built with Gin and produces structured JSON responses

* **Database Layer**
  - [x] PostgreSQL backend with well-defined schema
  - [x] Interface-based abstraction
  - [x] Connection pooling
  - [x] Transaction support

* **Session Recording**
  - [x] Captures all SSH I/O with timestamps
  - [x] Structured, replay-ready format
  - [x] Stored by session ID

#### Agent

- [x] Reverse SSH agent that dials the gateway
- [x] Auto-reconnect functionality
- [x] Machine registration/heartbeat
- [x] Agent key authentication

#### Client Tools

* **osh / ocp**
  - [x] Authenticates with the gateway
  - [x] Proxies to target machines / SCP via gateway

### Security & Access Control (v0.1)

- [x] SSH public key auth for users/agents
- [x] Relationship-Based Access Control (ReBAC)
- [x] Machine-specific grants, permission expiry, admin bypass
- [x] Temporary access request/approve/reject workflow
- [x] Audit logging via API

### v0.2 — API & Auth Foundations

- [x] REST API surface expansion
- [x] API key generation, hashing, validation, revoke/delete
- [x] Session-token auth for HTTP clients
- [x] `login/key` (SSH pubkey → API key) for CLI tools

### v0.3 — Client Workflows & Plugins

- [x] Dynamic `.so` plugin loader with lifecycle hooks *(superseded 2026-07-14, see "Compiled-in plugin platform" below)*
- [x] Slack notification plugin
- [x] Audit-logger plugin
- [x] Remote user specification (`user@machine`, `--user`, `remote_users`)
- [x] Client machine listing (`osh --list`)
- [x] Access request creation + polling from client
- [x] Admin CLI (`oadmin`) for approve/reject
- [x] Docker Compose local-dev stack (merged on master post-tag)

### Phase 1 Hardening (v0.3.1 — current)

* **SSH Host Key Verification**
  - [x] Replace `InsecureIgnoreHostKey` in client (`osh`/`ocp`)
  - [x] Replace `InsecureIgnoreHostKey` in agent
  - [x] TOFU (Trust On First Use) via `strict_host_key_checking: ask`
  - [x] Strict mode (`yes`) and insecure opt-out (`no`)
  - [x] Known hosts file management (`auth.known_hosts`)

* **API Authentication & Authorization**
  - [x] JWT bearer tokens (`Authorization: Bearer`, `POST /api/v1/public/login/token`)
  - [x] API key generation, validation, and management (from v0.2)
  - [x] Database-backed API key validation
  - [x] Rate limiting per user/IP on protected routes
  - [x] Login requires SSH public key verification (no passwordless username login)

* **Notifications & Integrations**
  - [x] Slack Incoming Webhook plugin
  - [x] SMTP email notification plugin
  - [x] Generic webhook notification plugin
  - [x] Access-request / access-granted hooks fired from API
  - [x] Post-auth hook fired after successful SSH auth
  - [ ] Template system for notification content
  - [ ] User notification preferences management

* **Agent Command Interface**
  - [x] Agent control commands: `orion:ping`, `orion:health`, `orion:status`, `orion:info`
  - [x] Admin API: `GET /api/v1/admin/agents/connected`, `POST /api/v1/admin/agents/:machine_id/command`, `POST /api/v1/admin/agents/install-script`
  - [ ] Richer remote management (reload config, drain, update)

* **Observability**
  - [x] Prometheus-format metrics at `GET /metrics`
  - [ ] OpenTelemetry tracing
  - [ ] Alerting system

---

### v0.4 — MFA, Admin UI, OpenFGA, Recording Hardening

* **MFA (TOTP)**
  - [x] TOTP enrollment (`POST /mfa/enroll`, `/mfa/confirm`)
  - [x] Backup codes (hashed, single-use)
  - [x] Disable with MFA proof
  - [x] Enforcement on API login paths (`totp_code`)
  - [x] `auth.mfa_required` blocks SSH until enrolled
  - [x] WebAuthn / hardware keys (YubiKey FIDO2)
  - [x] FIDO SSH public keys (`sk-ssh-ed25519@openssh.com`, etc.)
  - [x] OpenSSH agentless clients (`user+machine@gateway`)
  - [x] High-fidelity web console with roles, web terminal, file browser
  - [x] Certificate lifecycle / SSH CA (`docs/SSH_CA.md`)

* **OpenFGA**
  - [x] Optional OpenFGA HTTP client (`auth.openfga`)
  - [x] Check on permission paths with ReBAC fallback
  - [x] Write/delete tuples on grant/revoke
  - [x] Example model in `docs/openfga-model.fga`

* **Recording encryption & retention**
  - [x] AES-256-GCM at-rest encryption (`recording.encryption_key`)
  - [x] Decrypt on playback API
  - [x] Retention enforcement loop (`recording.retention_days`)
  - [ ] Compression

---

### Compiled-in plugin platform, ChatOps approvals & console theming (2026-07-14)

* **Plugin platform**
  - [x] Plugins compiled directly into the server binary, replacing dynamic `.so` loading — sidesteps Go plugin buildmode's CGO/same-toolchain/same-arch/same-libc constraints; server binary is static again
  - [x] Plugin enable/configure state moved to the database (`plugin_settings` table), editable at runtime with no restart and no YAML
  - [x] New admin API (`GET`/`PUT`/`POST /api/v1/admin/plugins...`) plus a Plugins settings page in the web console
  - [x] Each plugin declares a `ConfigSchema()` so the UI renders real form fields (text/bool/int/nested groups); secret fields are partially revealed (e.g. `xoxb****9f2c`) and reconciled back to the stored secret on save instead of requiring full re-entry
  - [x] `audit-logger` ships enabled and auto-configured by default; WebAuthn/FIDO2 enabled by default across Docker, qemu lab, and lab compose
  - [x] Build pipeline (`make build-server`, qemu lab scripts, GoReleaser) always rebuilds the embedded web console before producing a server binary, so it can no longer ship a stale committed UI snapshot

* **ChatOps**
  - [x] New `chatops-access-request` plugin posts access requests to Slack, Discord, Microsoft Teams, and Rocket.Chat
  - [x] Slack/Discord get native, signature-verified interactive Approve/Deny buttons; Teams/Rocket.Chat use signed magic links (documented limitation — no bot registration required)
  - [x] Calls back into Orion Belt's own approve/reject API

* **Console**
  - [x] Dark and light themes, switchable from a toggle on the login page and app shell; defaults to light, remembers an explicit choice
  - [x] Nav icons rewritten as inline SVG (currentColor + active-item glow) instead of static PNGs, so they follow either theme automatically

* **Docs**
  - [x] `docs/PLUGIN_DEVELOPMENT.md` rewritten for the compiled-in plugin model
  - [x] `docs/SRS-UI.md` palette/typography notes updated for dark/light theming

---

## Still open (post-v0.4)

- [ ] HashiCorp Vault integration
- [ ] Structured logs (Loki/ELK)
- [ ] OpenTelemetry tracing
- [ ] Notification templates / user preferences
- [ ] Recording compression
- [ ] Richer permission editor in UI
- [x] Machine CRUD in UI
- [x] Session recording playback + audit trail + user management in `/ui`
- [x] Web terminal sessions recorded with `source=web`
- [x] Timed cast (`.cast`) recordings + xterm playback; UI **Add agent** install scripts
- [x] Build version on binaries, `/health`, `/api/v1/version`, and UI
- [x] GPG signing for release checksums + APT/RPM repos (`make packaging-key`, `make repos`)

### Ops / release (in progress on this branch)

- [x] Go 1.26.5 + dependency bump; `govulncheck` 0-CVE gate (`make cve`, CI)
- [x] Native packages: deb / rpm / apk via GoReleaser + nFPM (`make packages`)
- [x] Multi-distro lab: Docker Compose + QEMU cloud images (Ubuntu, Alpine, openSUSE, Debian, Rocky)
- [x] QEMU lab clean/start pipeline + formal E2E QA plan (`docs/E2E_TEST_PLAN.md`)

## Roadmap — Later Phases

### v1.0.0 — call it stable (shipped)

Notes: [V1_RELEASE_CRITERIA.md](V1_RELEASE_CRITERIA.md), [API_STABILITY.md](API_STABILITY.md), [RELEASE_SMOKE.md](RELEASE_SMOKE.md) (`make release-smoke`).

### After 1.0 — identity and bigger features

- [ ] OIDC (Okta / Entra / Keycloak / Google / …)
- [ ] Map IdP groups to roles/grants; keep a local break-glass admin
- [ ] HA / DB replicas
- [x] JIT access (TTL, `access_type`, reject notify, stale expiry)
- [ ] Risk-based policies
- [ ] RBAC next to ReBAC
- [x] Live session watch
- [ ] Command filtering
- [ ] SAML / LDAP
- [ ] SIEM hooks
- [ ] Ticketing hooks

### After 1.0 — compliance-ish

- [ ] Compliance reporting packs
- [ ] Declarative policy engine
- [ ] Extra network segmentation proxies
- [x] Usage analytics (dashboard: access volume, approval latency, top targets)
- [ ] Anomaly / security analytics

### After 1.0 — other protocols / platform

- [ ] RDP / VNC / Kubernetes / DB proxies
- [ ] SDKs (Go, Python, TypeScript)
- [ ] Workflow automation
- [ ] AI/ML access analytics (maybe)
- [ ] More CLI work

---

## Technical Debt & Improvements

**Code Quality**

- [ ] Hit ~80% unit test coverage
- [ ] Integration test suites
- [x] End-to-end CVE gate (`e2e/cve`, `scripts/cve-check.sh`)
- [x] Multi-distro e2e lab (compose + QEMU) + [E2E test plan](E2E_TEST_PLAN.md)
- [x] Performance benchmarks (`pkg/server/bench_test.go`, nightly gate) — [BENCHMARKS.md](BENCHMARKS.md)
- [x] Architecture Decision Records (ADRs) — started (`docs/adr/`)

**Refactoring**

- [ ] Error handling standardization
- [x] Logging standardization (JSON slog)
- [ ] Config validation improvements
- [x] Complete remaining machine CRUD admin stubs
- [x] Session recording playback + audit trail + user management in `/ui`
- [x] Timed cast recordings + **Add agent** OS install scripts (`POST /admin/agents/install-script`)

**Documentation**

- [x] OpenAPI/Swagger specification (`docs/openapi/openapi.yaml`, `GET /api/v1/openapi.yaml`)
- [x] Web console SRS (`docs/SRS-UI.md`)
- [x] Deployment hardening guides (`docs/DEPLOYMENT_HARDENING.md`)
- [x] Plugin development guides
- [x] Contributing guidelines
- [x] Architecture documentation

---

## Version Milestones

* **v0.1:** Core SSH proxy, session recording, ReBAC
* **v0.2:** REST API, API keys / session auth
* **v0.3:** Plugins (dynamic), remote users, client access workflow, `oadmin`
* **v0.3.1:** Host key verification (TOFU), JWT, rate limits, email/webhook plugins, agent control commands, Prometheus metrics
* **v0.4:** MFA (TOTP + WebAuthn/FIDO), OpenSSH agentless clients (`user+machine@gateway`), role-aware `/ui` (terminal + files), optional OpenFGA, recording encryption + retention
* **v0.5:** Native packages (deb/rpm/apk) + GPG-signed repos, 0-CVE CI gate, multi-distro QEMU/Compose lab, React web console rebuild, web terminal session recording, setup wizard, OpenAPI + binary versioning, agent install-script UX
* **v0.6:** Docker Compose quickstart (server/agent), Commons Clause license, security hardening (file-browser command injection fix, remote-user exec impersonation, plugin hook timeouts, session cleanup)
* **v0.7:** Compiled-in plugin platform (replaced `.so` loading) with DB-backed live config + admin UI, `chatops-access-request` (Slack/Discord/Teams/Rocket.Chat), dark/light console theming, inline SVG nav
* **v0.8:** SSH Certificate Authority (user/host certs, agent Host-cert identity, renewal, revoke), challenge-response pubkey API login, browser bootstrap codes, in-app notification bell, password+TOTP login — see `docs/SSH_CA.md`
* **v0.9:** Richer permissions UI, recording compression, live session watch, notification prefs, JIT polish, ops docs (folded into the v1.0 cut)
* **v1.0:** Stable SSH PAM — `/api/v1` compatibility promise, ops docs, release smoke; see [V1_RELEASE_CRITERIA.md](V1_RELEASE_CRITERIA.md)
* **Later:** OIDC (then SAML/LDAP), HA, other protocols, SDKs, compliance packs, SIEM/ticketing

---

## Contribution

Helpful directions right now:

- OIDC + group mapping
- Agent remote management (drain, reload, update)
- HA, RDP/VNC, SDKs, tests / coverage

---

## Notes

Tags and merged PRs are the history. **v1.0.0** is the first release we call stable SSH PAM (API notes + ops docs + smoke). OIDC and friends come after.

**Last Updated:** 2026-07-14 (v1.0.0)  
**Maintainer:** Mohamed Zrouga ([@zrougamed](https://github.com/zrougamed))
