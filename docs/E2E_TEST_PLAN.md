# End-to-End Test Plan — QEMU Multi-Distro Lab

| Field | Value |
|-------|--------|
| **Document ID** | `QA-E2E-QEMU-001` |
| **Product** | Orion Belt |
| **Scope** | QEMU lab: clean → start → admin UI → agents → RBAC users → OpenSSH gateway |
| **Type** | System / integration E2E |
| **Automation entry** | `make lab-qemu-start` |
| **Related** | [lab/README.md](../lab/README.md), [openssh-clients.md](openssh-clients.md), [PACKAGING.md](PACKAGING.md), [RELEASE_SMOKE.md](RELEASE_SMOKE.md) |
| **Status** | Active |
| **Last updated** | 2026-07-14 |

---

## 1. Purpose

Validate that a fresh Orion Belt gateway can be brought up under QEMU, that multi-distro agents register and stay connected, that role-based users receive correct machine grants, and that a host OpenSSH client can open sessions through the gateway.

## 2. Scope

### In scope

- Lab clean / wipe semantics
- Server VM boot, PostgreSQL, Orion server process
- Admin bootstrap and `/ui` login (SSH public key)
- Agent key collection, API registration, agent reconnect
- Demo users: `admin`, `operator`, `auditor`, `alice`, `bob`
- OpenSSH `user+machine@gateway` access from the lab host
- Basic negative checks (unauthorized machine)

### Out of scope

- WebAuthn / YubiKey hardware flows
- OpenFGA external authorization
- HA / multi-gateway
- Package install on bare metal (covered by packaging docs)
- Performance / load testing

## 3. References

| ID | Artifact |
|----|----------|
| LAB | `lab/qemu/start.sh`, `clean.sh`, `connect-agents.sh`, `seed-users.sh` |
| API | `POST /api/v1/public/register/*`, `POST /api/v1/public/login`, `POST /api/v1/admin/permissions` |
| UI | `http://127.0.0.1:8080/ui` |
| SSH | Port `2222` on host → server VM; docs/openssh-clients.md |

## 4. Test environment

### 4.1 Host prerequisites

| Requirement | Notes |
|-------------|--------|
| OS | Linux x86_64 (Ubuntu/Fedora recommended) |
| Packages | `qemu-system-x86`, `qemu-utils`, `cloud-image-utils` or `genisoimage`, `openssh-client`, `curl`, `python3` |
| Go | **1.26.5+** (for building binaries into `dist/`) |
| Nested virt | `/dev/kvm` preferred; TCG works but is slower |
| Disk / RAM | ~8–12 GB free disk for images; ≥8 GB host RAM recommended |
| Network | Outbound HTTPS to download cloud images (first run) |

### 4.2 Lab topology

```
 Host
  ├─ :2222 ──hostfwd──► Server VM (Ubuntu)  orion-belt-server + PostgreSQL
  ├─ :8080 ──hostfwd──► Server VM API + /ui
  ├─ :2200 ──hostfwd──► Server VM sshd (mgmt)
  ├─ :2201–2204 ───────► Agent VM sshd (mgmt)
  └─ :8765 ────────────► Host HTTP (dist/ packages)
         ▲
 Agent VMs dial 10.0.2.2:2222 (QEMU usermode gateway → host → server)
```

| Guest | Mgmt SSH | Role |
|-------|----------|------|
| Ubuntu server | `:2200` ubuntu | Gateway |
| Alpine | `:2201` alpine | Agent |
| openSUSE | `:2202` opensuse | Agent |
| Debian | `:2203` debian | Agent |
| Rocky | `:2204` rocky | Agent |

### 4.3 Test data (created by automation)

| Principal | Role | Credentials | Machine grants |
|-----------|------|-------------|----------------|
| `admin` | admin | `lab/credentials/admin_ed25519` | All (admin bypass) |
| `operator` | operator | `lab/credentials/operator_ed25519` | All registered agents |
| `auditor` | auditor | `lab/credentials/auditor_ed25519` | None |
| `alice` | user | `lab/credentials/alice_ed25519` | `agent-alpine` only |
| `bob` | user | `lab/credentials/bob_ed25519` | `agent-debian` (ssh) only |

## 5. Entry / exit criteria

### Entry criteria

- [ ] Host packages installed
- [ ] Working directory is repo root
- [ ] Ports `2222`, `8080`, `2200–2204`, `8765` free (or previous lab stopped)
- [ ] Tester can run `make` and has network for first image download

### Exit criteria (suite pass)

- [ ] All **Priority P0** cases Pass
- [ ] No P0 defects open
- [ ] Credentials and howto printed; `lab/credentials/USERS.txt` present
- [ ] At least one agent shows connected and one OpenSSH session succeeds

### Suspension / abort

- Host OOM / disk full
- Cloud image download unavailable > 30 minutes
- Server API never becomes ready within `ORION_WAIT_SECS` (default 600s)

## 6. Pass / fail / blocked definitions

| Result | Definition |
|--------|------------|
| **Pass** | Actual result matches Expected result; no unexpected errors in cited logs |
| **Fail** | Expected result not met, or FATAL/error that blocks the step |
| **Blocked** | Cannot execute due to unmet dependency (prior case failed, env issue) |
| **Pass with notes** | Goal met with acceptable workaround documented |

## 7. Test suite execution

### 7.1 Automated primary path (recommended)

| Step | Action | Expected result |
|------|--------|-----------------|
| 1 | `make lab-qemu-clean` | VMs stopped; `lab/qemu/run`, `lab/qemu/images`, `lab/credentials` removed (unless KEEP_* set) |
| 2 | `make lab-qemu-start` | Pipeline completes without shell exit ≠ 0 |
| 3 | Review console output | Admin UI instructions + `USERS.txt` content + SSH howto printed |
| 4 | Spot-check files | `lab/credentials/admin_ed25519.pub` and `USERS.txt` exist |

**Duration (guide):** 15–45 minutes depending on image download and KVM vs TCG.

### 7.2 Faster re-run (images cached)

```bash
KEEP_IMAGES=1 make lab-qemu-start
```

---

## 8. Test cases

### TC-QEMU-001 — Full clean wipes lab state

| | |
|--|--|
| **Priority** | P0 |
| **Type** | Functional |
| **Preconditions** | Lab previously started (optional); artifacts may exist under `lab/qemu/run`, `images`, `credentials` |

**Steps**

1. Run `make lab-qemu-clean`.
2. List `lab/qemu/run`, `lab/qemu/images`, `lab/credentials`.
3. Confirm no `qemu-system-x86_64 -name server` (or agent-*) processes.

**Expected results**

1. Command exits 0.
2. Directories empty or absent (recreated empty `run/logs` / `images` OK).
3. No lab QEMU processes remain.

**Actual / evidence:** _(tester fills)_  
**Result:** ☐ Pass ☐ Fail ☐ Blocked

---

### TC-QEMU-002 — Clean does not require flags to remove images

| | |
|--|--|
| **Priority** | P0 |
| **Type** | Functional |

**Steps**

1. Ensure at least one file exists under `lab/qemu/images/` (from a prior download).
2. Run `make lab-qemu-clean` with **no** `KEEP_IMAGES`.
3. Check `lab/qemu/images/`.

**Expected results**

1. Cloud images are deleted.
2. Console logs a line about removing cloud images.

**Result:** ☐ Pass ☐ Fail ☐ Blocked

---

### TC-QEMU-003 — Lab start brings API and gateway online

| | |
|--|--|
| **Priority** | P0 |
| **Type** | Functional / Integration |
| **Preconditions** | TC-QEMU-001 completed or ports free |

**Steps**

1. Run `make lab-qemu-start` (or continue from a start already running).
2. From host: `curl -sS -o /dev/null -w '%{http_code}\n' http://127.0.0.1:8080/metrics`
3. From host: `ss -ltn | grep -E ':2222|:8080|:2200'`
4. SSH mgmt: `./lab/qemu/ssh.sh server -- 'ps aux | grep orion-belt-server | grep -v grep'`

**Expected results**

1. Start reaches “API is up” (or completes pipeline).
2. HTTP status `200` from `/metrics`.
3. Host listens on `2222`, `8080`, `2200`.
4. `orion-belt-server` process is running inside the server VM.

**Result:** ☐ Pass ☐ Fail ☐ Blocked

---

### TC-QEMU-004 — Admin bootstrap and UI login

| | |
|--|--|
| **Priority** | P0 |
| **Type** | Functional / UI |
| **Preconditions** | API up (TC-QEMU-003) |

**Steps**

1. Confirm `lab/credentials/admin_ed25519` (private), `.pub`, and `UI-LOGIN.txt` exist.
2. Open `http://127.0.0.1:8080/ui` — check the offered login methods.
3. Run `./bin/osh -u admin --api-endpoint http://127.0.0.1:8080 -i lab/credentials/admin_ed25519 login --code`.
4. Open the printed URL, or paste the code into **Device code** on the login screen.
5. Re-submit the same code a second time.

**Expected results**

1. Files exist; pubkey is a single `ssh-ed25519 …` line.
2. Login page loads (branded Orion Belt UI) offering **Password**, **Security key**, **Device code** — there is no SSH-public-key field, by design.
3. `osh` authenticates via key challenge-response and prints a 10-char code plus a `/ui/bootstrap?code=…` URL with a 5-minute expiry.
4. Session is established; console shows machines / ops views appropriate for admin. First login lands on the blocking "create a password + enrol TOTP" gate.
5. Rejected — codes are single-use.

**Result:** ☐ Pass ☐ Fail ☐ Blocked

---

### TC-QEMU-005 — Agents registered and connected

| | |
|--|--|
| **Priority** | P0 |
| **Type** | Integration |
| **Preconditions** | Start pipeline finished connect-agents step |

**Steps**

1. List `lab/qemu/run/agent-*.pub` — expect keys for guests that finished cloud-init.
2. In UI (as admin) or API: list machines; note `agent-alpine`, `agent-debian`, etc.
3. Check agent log: `./lab/qemu/ssh.sh alpine -- 'doas tail -20 /var/log/orion-agent.log'`  
   (Debian/SUSE/Rocky: use `sudo` instead of `doas` if present.)
4. Optional: `GET /api/v1/admin/agents/connected` with admin Bearer/session token.
5. Optional (UI): **Add agent** → generate install script for a throwaway name → confirm `201` from `POST /api/v1/admin/agents/install-script` and that the script contains `agent.yaml` + private key + package download URL.

**Expected results**

1. At least Alpine (and preferably Debian) pubkey files present after a healthy boot.
2. Matching machine records exist in the UI/API.
3. Log contains `Connected to server: 10.0.2.2:2222` and `handleChannels started` (not only FATAL EOF without later success).
4. Connected agents list is non-empty for registered running agents.
5. (If step 5) Install-script response includes `script`, `machine_id`, and `public_key`.

**Notes:** Guests still in cloud-init may be Blocked/retest; re-run `make lab-qemu-connect-agents AGENTS="…"`.

**Result:** ☐ Pass ☐ Fail ☐ Blocked

---

### TC-QEMU-006 — RBAC seed users and grants

| | |
|--|--|
| **Priority** | P0 |
| **Type** | Security / Functional |
| **Preconditions** | `seed-users.sh` completed; `lab/credentials/USERS.txt` present |

**Steps**

1. Read `lab/credentials/USERS.txt`.
2. Confirm key files: `operator_ed25519`, `auditor_ed25519`, `alice_ed25519`, `bob_ed25519` (+ `.pub`).
3. UI login as `alice` with `alice_ed25519.pub` — verify visible machines include Alpine (or only Alpine).
4. UI login as `bob` with `bob_ed25519.pub` — verify Debian access, not Alpine (as applicable to UI listing).
5. UI login as `auditor` — verify login works; machine session start to Alpine should be denied or unavailable.

**Expected results**

1. Summary table matches §4.3.
2. All four demo keypairs exist.
3. Alice can operate on `agent-alpine` only.
4. Bob limited to `agent-debian`.
5. Auditor has no machine grants for interactive access.

**Result:** ☐ Pass ☐ Fail ☐ Blocked

---

### TC-QEMU-007 — OpenSSH from host through gateway (alice → alpine)

| | |
|--|--|
| **Priority** | P0 |
| **Type** | Integration / End-user |
| **Preconditions** | Alice granted on `agent-alpine`; agent connected |

**Steps**

1. From host run:
   ```bash
   ssh -i lab/credentials/alice_ed25519 -p 2222 \
     -o StrictHostKeyChecking=accept-new \
     alice+agent-alpine@127.0.0.1
   ```
2. In the remote session, run `hostname` or `uname -a`.
3. Exit the session.

**Expected results**

1. SSH authenticates with Alice’s key (may prompt to trust gateway host key once).
2. Session lands on the Alpine agent environment (hostname/`uname` consistent with Alpine guest).
3. Clean disconnect.

**Result:** ☐ Pass ☐ Fail ☐ Blocked

---

### TC-QEMU-008 — OpenSSH negative: alice cannot use bob’s machine

| | |
|--|--|
| **Priority** | P1 |
| **Type** | Security |
| **Preconditions** | TC-QEMU-006; `agent-debian` registered |

**Steps**

1. Attempt:
   ```bash
   ssh -i lab/credentials/alice_ed25519 -p 2222 \
     -o BatchMode=yes -o ConnectTimeout=10 \
     alice+agent-debian@127.0.0.1
   ```

**Expected results**

1. Connection fails or session is rejected (permission / access denied). Alice must **not** get an interactive shell on Debian.

**Result:** ☐ Pass ☐ Fail ☐ Blocked

---

### TC-QEMU-009 — Admin OpenSSH to any registered agent

| | |
|--|--|
| **Priority** | P1 |
| **Type** | Functional |

**Steps**

1. `ssh -i lab/credentials/admin_ed25519 -p 2222 admin+agent-alpine@127.0.0.1`
2. If Debian connected: `ssh -i lab/credentials/admin_ed25519 -p 2222 admin+agent-debian@127.0.0.1`

**Expected results**

1–2. Sessions succeed for registered connected agents.

**Result:** ☐ Pass ☐ Fail ☐ Blocked

---

### TC-QEMU-010 — Operator role can use granted agents

| | |
|--|--|
| **Priority** | P1 |
| **Type** | Functional / RBAC |

**Steps**

1. UI or SSH as `operator` with `operator_ed25519`.
2. Open session to `agent-alpine` (and another registered agent if up).

**Expected results**

1. Operator authenticates.
2. Sessions succeed on agents granted in seed (all registered).

**Result:** ☐ Pass ☐ Fail ☐ Blocked

---

### TC-QEMU-011 — KEEP_IMAGES preserves downloads

| | |
|--|--|
| **Priority** | P2 |
| **Type** | Regression |

**Steps**

1. After a successful image download, note size of `lab/qemu/images/`.
2. `KEEP_IMAGES=1 make lab-qemu-clean`
3. Confirm images still present; `run/` and credentials cleared (unless KEEP_CREDS).

**Expected results**

1–3. Images retained; VM overlays removed; console mentions keeping images.

**Result:** ☐ Pass ☐ Fail ☐ Blocked

---

### TC-QEMU-012 — CVE gate (tooling E2E, non-QEMU)

| | |
|--|--|
| **Priority** | P1 |
| **Type** | Security / CI |
| **Preconditions** | Go 1.26.5+ |

**Steps**

1. `make cve`
2. Optional: `ORION_CVE_E2E=1 go test ./e2e/cve/ -v`

**Expected results**

1. `govulncheck` reports **0 affecting vulnerabilities** for source and binaries; exit 0.
2. Test Pass.

**Result:** ☐ Pass ☐ Fail ☐ Blocked

---

## 9. Traceability matrix

| Requirement / capability | Test cases |
|--------------------------|------------|
| Clean lab / wipe images by default | TC-QEMU-001, 002, 011 |
| Server + API + UI up | TC-QEMU-003, 004 |
| Multi-distro agents connected | TC-QEMU-005 |
| RBAC / ReBAC demo users | TC-QEMU-006, 008, 010 |
| Host OpenSSH via gateway | TC-QEMU-007, 009 |
| Zero-CVE dependency gate | TC-QEMU-012 |

## 10. Defect reporting template

When filing a fail:

1. **Case ID** (e.g. TC-QEMU-007)
2. **Environment** (host OS, KVM/TCG, `KEEP_*` flags)
3. **Steps** actually performed
4. **Expected vs actual**
5. **Evidence:** console snippet, `lab/qemu/run/logs/*.log`, guest `orion-agent.log`, server `/var/log/orion-belt/server.log`
6. **Severity:** Blocker / Major / Minor / Cosmetic

## 11. Sign-off

| Role | Name | Date | Outcome |
|------|------|------|---------|
| Executor | | | ☐ Pass ☐ Fail |
| Reviewer | | | ☐ Approved ☐ Rework |

---

## Appendix A — Useful commands

```bash
make lab-qemu-clean
make lab-qemu-start
KEEP_IMAGES=1 make lab-qemu-start
SKIP_CLEAN=1 make lab-qemu-start

make lab-qemu-connect-agents
make lab-qemu-connect-agents AGENTS="alpine debian"
make lab-bootstrap-admin

./lab/qemu/ssh.sh server
./lab/qemu/ssh.sh alpine -- 'doas tail -50 /var/log/orion-agent.log'

curl -sS http://127.0.0.1:8080/metrics | head
cat lab/credentials/USERS.txt
cat lab/credentials/admin_ed25519.pub
```

## Appendix B — Known timing hazards

| Symptom | Likely cause | Action |
|---------|--------------|--------|
| Agent log only `handshake failed: EOF` | Agent started before server listen | Re-run `make lab-qemu-connect-agents`; agent cloud-init now retries |
| Alpine restart fails with `sudo` | Alpine uses `doas` | Fixed in helpers; use `doas` on guest |
| Rocky/openSUSE pubkey missing | Guest still booting | Wait; retry connect with `AGENTS=…` |
| `/ui` login invalid credentials | Wrong pubkey paste / wrong user | Paste full single-line `.pub` for that username |
