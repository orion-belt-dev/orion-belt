# SRS — Orion Belt Web Console (UI)

**Document type:** Software Requirements Specification (UI)  
**Scope:** React SPA under `web/ui/` (built into `web/static/`, embedded at `/ui/`)  
**Base URL:** `/ui/` (served by the gateway; redirects from `/` and `/admin`)  
**API:** `/api/v1/*` — see [openapi/openapi.yaml](openapi/openapi.yaml)  
**Status:** React console Phase 2 — **v1.0.0**  
**Last updated:** July 2026 (live watch, permissions all-grants/edit, notification prefs + admin notification policy, admin notification templates, usage analytics dashboard)

---

## 1. Purpose

The Orion Belt web console is the primary operator surface for privileged access: authenticate, request and approve access, open recorded web terminals, browse remote files, manage users/machines/agents, and inspect audit/session trails — without leaving the browser.

This SRS is the acceptance baseline for UI regressions and for matching OpenAPI coverage.

---

## 2. Goals & non-goals

### Goals

- Vite + React 19 + TypeScript console with role-aware navigation
- High-contrast console with dark and light themes (Inter/Space Grotesk + JetBrains Mono)
- First-class login with SSH pubkey, optional TOTP, and WebAuthn/FIDO2
- Live PTY terminal over WebSocket with session recording (`source=web`)
- Timed cast session playback (xterm), audit log browser, user/machine admin CRUD
- **Add agent** install-script generator (OS-specific)
- Dashboard usage analytics with selectable window and auto-refresh
- Visible **build version** so operators can confirm shipped features
- In-app **notification bell** (access-request approvals and similar)
- Per-user **notification preferences** bounded by an admin policy: channels the
  policy disallows render disabled, admin-mandated events render locked on, and
  a save that had to be adjusted says so (Security → Notifications). Admins edit
  the policy itself on the Plugins page.
- Admin-editable **notification templates**: the title and body of each event's
  copy, with the variables that event supplies and a reset to the built-in
  wording (Plugins page). Edits apply to the next notification delivered.
- Production assets embedded via `go:embed` (`make build-ui` → `web/static`)

### Non-goals (deferred)

- Fine-grained UI RBAC beyond role-gated nav (API still enforces ReBAC)
- Collaborative cursors / multi-viewer chat over live sessions (read-only watch is supported)
- Full spreadsheet-style permission matrix (All grants + edit/revoke is supported)
- SSH CA admin panel in the UI (export/list/revoke via `oadmin` / API today — see [SSH_CA.md](SSH_CA.md))
- Heavy third-party admin kits (MUI/Ant) — prefer owned CSS tokens

---

## 3. Personas & roles

| Role | Nav access (implemented) | Notes |
|------|--------------------------|-------|
| **admin** | Dashboard, Setup, Requests, Machines, Terminal, Files, Sessions, Users, Permissions, Agents, Add agent, Audit, Security | Full console |
| **operator** | Same as admin | Approvals + admin API allowed |
| **auditor** | Dashboard, Sessions, Users (read), Audit, Security | No terminal/files/agents/setup |
| **user** | Machines, Terminal, Files, Sessions, Requests, Audit, Security | Self-service access + own security |

Role uses `EffectiveRole`: explicit `admin`/`operator`/`auditor`, else `is_admin → admin`, else `role`/`user`.

---

## 4. Information architecture

```
/ui/
├── /login
└── App shell
    ├── Side nav (role-filtered) + version in footer
    └── Routes
        ├── / (dashboard for admin/operator/auditor)
        ├── /setup
        ├── /catalog (access catalog; landing page for role `user`)
        ├── /requests
        ├── /machines
        ├── /terminal
        ├── /files
        ├── /sessions (+ cast playback)
        ├── /users
        ├── /agents
        ├── /add-agent
        ├── /audit
        └── /security
```

---

## 5. Visual design system

### 5.1 Brand & composition

- **Brand first:** Login hero is “Orion *Belt*” at display size; tagline secondary; version under tagline.
- **One composition:** Login is a centered stage (not a dashboard). App shell is nav + workspace.
- **Atmosphere:** dark navy (or light) base with a subtle grid + radial washes.
- **Accent:** Blue `#146ef5`, with a lighter `#33b8ff` for secondary/glow signals.

### 5.2 Tokens

Defined in `web/ui/src/styles/theme.css` (`:root` CSS variables).

### 5.3 Stack

| Layer | Choice |
|-------|--------|
| Bundler | Vite |
| UI | React 19 + TypeScript |
| Routing | React Router (`basename=/ui`) |
| Data | TanStack Query |
| Terminal / cast | `@xterm/xterm` + FitAddon |

---

## 6. Functional requirements

Parity with the original console FRs remains the Phase 1 bar:

- Login (pubkey, TOTP, WebAuthn)
- Role-filtered shell + version chip
- Dashboard / setup / requests / machines / terminal / files
- Access catalog: capabilities filtered to the logged-in user's relationships (grants, requests, sessions, shared machine tags), with one-click request from the row
- Dashboard usage analytics: access volume, approval latency (avg/p50/p95), and top targets over a selected window
- Sessions list + timed cast playback + download
- Users / agents / **Add agent** install script
- Audit / security (MFA enroll/confirm, SSH keys list)

### Build & ship

| ID | Requirement | Status |
|----|-------------|--------|
| FR-BUILD-01 | `make build-ui` produces `web/static/index.html` + assets | Done |
| FR-BUILD-02 | Server embeds `web/static` at `/ui/` | Done |
| FR-BUILD-03 | `npm run dev` proxies API to `:8080` for local UI work | Done |

---

## 7. Acceptance checklist (QA)

1. `make build-ui && go build -o bin/orion-belt-server ./cmd/server`
2. Open `/ui/` — login brand + version visible
3. Admin login shows **Add agent** under Agents
4. Terminal connect → disconnect → Sessions playback works for `.cast`
5. Add agent generates install script (`POST /admin/agents/install-script`)
6. Role `user` cannot open `/add-agent` or `/agents`
7. `/catalog` lists only capabilities tied to the signed-in user; **Request** on a row files the access request and the row turns *pending*

---

## 8. Implementation notes

- **Source:** `web/ui/`
- **Shipped assets:** `web/static/` (built; embedded by `web/embed.go`)
- **RBAC in UI** is navigation gating only; the API remains authoritative.
- Phase 2 delivered: permission editor (`/permissions`), command palette (⌘/Ctrl+K), dashboard agent health, session search.
- Phase 2 delivered: permission editor (`/permissions`), command palette (⌘/Ctrl+K), dashboard agent health, usage analytics (`GET /dashboard/usage`), session search.
- Phase 3 candidates: live session join, richer audit filters, MFA enrollment polish.
