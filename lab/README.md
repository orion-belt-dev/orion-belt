# Multi-distro test lab

The lab runs a gateway and agents on current Linux releases in one of two ways:

| Lab | Use it for | Command |
|-----|-------------|---------|
| Docker Compose (`lab/compose`) | Quick smoke tests in CI or on a laptop | `make lab-compose-up` |
| QEMU cloud images (`lab/qemu`) | Real packages and cloud-init on full VMs | `make lab-qemu-start` |

Test procedures are in [docs/E2E_TEST_PLAN.md](../docs/E2E_TEST_PLAN.md).

## Signing in to the console

Lab accounts have no password. You sign in with a username and SSH key.

Once the API is up, create the admin account (`make lab-qemu-start` does this
for you):

```bash
make lab-bootstrap-admin
```

This only works on an empty install, because the server allows an
unauthenticated registration only for the first account. Re-running it later
is harmless: the script reports that the server is already bootstrapped.

The console cannot sign a login challenge with a key file, so sign in through
`osh`, which signs the challenge and opens the browser already signed in:

```bash
make build-client   # if bin/osh isn't built yet
./bin/osh -u admin --api-endpoint http://127.0.0.1:8080 \
    -i lab/credentials/admin_ed25519 login
```

Add `--code` to print a one-time code instead of opening a browser, and enter
it under **Device code** on the login screen. Codes are single-use and expire
after five minutes.

The admin's details are in `lab/credentials/UI-LOGIN.txt`, and the demo users
in `lab/credentials/USERS.txt`.

### Admin credentials for lab scripts

Creating users and registering agents requires an admin or operator
credential. `seed-users.sh` and `register-agents.sh` obtain one themselves:
they call `osh` with `lab/credentials/admin_ed25519` to create an API key that
expires after a day, building `bin/osh` first if needed. To use your own
credential instead, set `ORION_API_KEY` or `ORION_SESSION_TOKEN` before running
`register-agents.sh`.

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

# Cleans, boots the VMs, creates the admin, connects agents, seeds demo users, prints SSH examples
make lab-qemu-start

# Re-run without wiping images (faster after first download):
KEEP_IMAGES=1 make lab-qemu-start

# Skip clean entirely (reuse running disks):
SKIP_CLEAN=1 make lab-qemu-start
```

`make lab-qemu-start` prints admin/demo credentials under `lab/credentials/` and OpenSSH examples for this host.

Record results against the test plan in [docs/E2E_TEST_PLAN.md](../docs/E2E_TEST_PLAN.md) (TC-QEMU-001 to TC-QEMU-012).

### Connect agents to the running server

Agents connect to `10.0.2.2:2222`, but the server must know their public keys
first:

```bash
make lab-qemu-connect-agents
# subset only:
make lab-qemu-connect-agents AGENTS="alpine debian"
```

That runs:

1. `lab/qemu/collect-agent-keys.sh` connects to each guest and saves its key as `run/<name>.pub`.
2. `lab/qemu/register-agents.sh` registers each key with `POST /api/v1/public/register/agent`, as the lab admin.
3. `lab/qemu/restart-agents.sh` restarts `orion-belt-agent` so it reconnects.

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
- Agent VMs connect to `10.0.2.2:2222`, which QEMU user-mode networking routes through the host to the server VM.
- Management SSH: server `:2200`, agents `:2201`–`:2204`.

`dist/` is served over HTTP on `:8765` so cloud-init can install packages or raw binaries.
Outside the lab you can run the same mirror with `make serve-packages`, or use the public Pages URL `https://orion-belt-dev.github.io/packages`.

Images: `lab/qemu/images/` (gitignored). Overlays/logs: `lab/qemu/run/`. Credentials: `lab/credentials/` (gitignored).

## CVE e2e gate

```bash
make cve
ORION_CVE_E2E=1 go test ./e2e/cve/ -v
```

Requires Go **1.26.6+** (see `go.mod`). Mapped as **TC-QEMU-012** in [docs/E2E_TEST_PLAN.md](../docs/E2E_TEST_PLAN.md).
