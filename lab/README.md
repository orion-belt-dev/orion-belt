# Multi-distro test lab

Two ways to run the server and attach agents across current Linux releases:

| Lab | When to use | Command |
|-----|-------------|---------|
| **Docker Compose** (`lab/compose`) | Fast CI / laptop smoke test | `make lab-compose-up` |
| **QEMU cloud images** (`lab/qemu`) | Near-native packages + cloud-init | `make lab-qemu-start` |

**QA / E2E procedures (professional test plan):** [docs/E2E_TEST_PLAN.md](../docs/E2E_TEST_PLAN.md)

## Admin UI login (`/ui`)

There is **no password**. Sign-in is **username + SSH public key**.

After the API is up, bootstrap creates an admin automatically (also part of `make lab-qemu-start`):

```bash
make lab-bootstrap-admin
```

Then sign in with the CLI — the console has no public-key field, so `osh` does
the SSH-key challenge-response and opens the browser already signed in:

```bash
make build-client   # if bin/osh isn't built yet
./bin/osh -u admin --api-endpoint http://127.0.0.1:8080 \
    -i lab/credentials/admin_ed25519 login
```

Add `--code` to print a one-time code instead of opening a browser, then paste
it into **Device code** on the login screen (single-use, 5 min TTL).

Helper details: `lab/credentials/UI-LOGIN.txt`. Demo users: `lab/credentials/USERS.txt`.

## Current OS images (latest pins)

### Docker Compose agents

| Service | Base image |
|---------|------------|
| `agent-ubuntu` | `ubuntu:25.04` |
| `agent-alpine` | `alpine:3.22` |
| `agent-suse` | `opensuse/leap:16.0` |
| `agent-debian` | `debian:trixie-slim` (Debian 13) |
| `agent-rocky` | `rockylinux/rockylinux:10` |

### QEMU cloud images (`lab/qemu/distros.yaml`)

| Guest | Image | Notes |
|-------|-------|-------|
| Server | Ubuntu 24.04 LTS minimal **daily/current** | freshest noble builds |
| Agent | Alpine **3.22.x** nocloud (auto-resolved to newest patch) | `download-images.sh` picks latest |
| Agent | openSUSE **Tumbleweed** Minimal VM Cloud | rolling |
| Agent | Debian **13 (trixie)** genericcloud | `.../trixie/latest/...` |
| Agent | Rocky Linux **9** GenericCloud `.latest` | official Rocky cloud |

Refresh cached downloads:

```bash
ORION_REFRESH_IMAGES=1 make lab-qemu-images
```

## Docker Compose lab (no QEMU)

```bash
make lab-compose-up
# Gateway: localhost:2222  API: localhost:8080  UI: /ui
# Admin key: lab/credentials/admin_ed25519.pub
make lab-compose-down
```

Register `lab/compose/agent-keys/agent_key.pub` with the server before agents authenticate.

## QEMU lab

### Host dependencies

```bash
# Debian/Ubuntu host
sudo apt install qemu-system-x86 qemu-utils cloud-image-utils genisoimage openssh-client curl

# Fedora
sudo dnf install qemu-system-x86 qemu-img cloud-utils genisoimage openssh-clients curl
```

KVM is used when `/dev/kvm` exists; otherwise TCG (slower).

### Clean & full start

```bash
# Wipe VMs, overlays, downloaded images, and lab credentials (default)
make lab-qemu-clean

# Clean (default) → boot → admin → connect agents → seed RBAC users → SSH howto
make lab-qemu-start

# Re-run without wiping images (faster after first download):
KEEP_IMAGES=1 make lab-qemu-start

# Skip clean entirely (reuse running disks):
SKIP_CLEAN=1 make lab-qemu-start
```

`make lab-qemu-start` prints admin/demo credentials under `lab/credentials/` and OpenSSH examples for this host.

**Execute and record results using the formal plan:** [docs/E2E_TEST_PLAN.md](../docs/E2E_TEST_PLAN.md) (TC-QEMU-001 … TC-QEMU-012).

### Connect agents to the running server

Agents dial `10.0.2.2:2222`, but the server must know their public keys first:

```bash
make lab-qemu-connect-agents
# subset only:
make lab-qemu-connect-agents AGENTS="alpine debian"
```

That runs:

1. `lab/qemu/collect-agent-keys.sh` — SSH into each guest, save `run/<name>.pub`
2. `lab/qemu/register-agents.sh` — `POST /api/v1/public/register/agent`
3. `lab/qemu/restart-agents.sh` — restart `orion-belt-agent` so it reconnects

Helpers:

```bash
make lab-qemu-update                              # rebuild + push bins + reload server/agents
make lab-qemu-update AGENTS="server"              # server VM only
make lab-qemu-update AGENTS="alpine rocky"        # selected agents
SKIP_BUILD=1 make lab-qemu-update                 # push existing dist/ without rebuilding
make lab-qemu-restart                             # reboot all VMs, keep disks
make lab-qemu-restart VMS="server"                # one instance
make lab-qemu-restart VMS="alpine rocky"
./lab/qemu/ssh.sh alpine                          # shell into guest
./lab/qemu/ssh.sh alpine -- 'sudo tail -f /var/log/orion-agent.log'
./lab/qemu/ssh.sh server                          # server VM (:2200)
make lab-bootstrap-admin                          # UI admin if needed
```

Inventory: `lab/qemu/agents.conf`.

Or bootstrap admin alone once the API answers:

```bash
make lab-bootstrap-admin
```

Networking:

- Server VM publishes host ports `2222` (gateway SSH) and `8080` (API).
- Agent VMs dial **`10.0.2.2:2222`** (QEMU usermode → host → server).
- Management SSH: server `:2200`, agents `:2201`–`:2204`.

`dist/` is served over HTTP on `:8765` so cloud-init can install packages or raw binaries.

Images: `lab/qemu/images/` (gitignored). Overlays/logs: `lab/qemu/run/`. Credentials: `lab/credentials/` (gitignored).

## CVE e2e gate

```bash
make cve
ORION_CVE_E2E=1 go test ./e2e/cve/ -v
```

Requires Go **1.26.5+** (see `go.mod`). Mapped as **TC-QEMU-012** in [docs/E2E_TEST_PLAN.md](../docs/E2E_TEST_PLAN.md).
