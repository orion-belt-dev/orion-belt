---
sidebar_position: 1
title: Installation
---

# Installation

Install from the signed packages: they come from
[pkgs.orion-belt.dev](https://pkgs.orion-belt.dev) and upgrade with the rest of
the system. Docker and building from source are there when packages don't fit.

:::tip Fastest path
**[Get started in 10 minutes](./get-started)** walks through the server, your
computer and an agent for your operating system, from the packages.
:::

## System requirements

| Component | CPU | RAM | Disk | OS |
| --- | --- | --- | --- | --- |
| Server | 2+ cores | 4GB+ | 20GB+ | Linux (Ubuntu 20.04+, RHEL 8+, …) |
| Agent | 1+ core | 512MB+ | 10GB+ | Linux (any modern distro) |
| Client | Any | 256MB | 100MB | Linux, macOS, Windows |

## Packages (recommended)

| Package | Install on | Systems |
| --- | --- | --- |
| `orion-belt` | The server | Debian, Ubuntu, Fedora, RHEL and clones, openSUSE, Alpine, Arch |
| `orion-belt-agent` | Each machine to reach | The same Linux systems |
| `orion-belt-tools` (`osh`, `ocp`, `oadmin`) | Your computer | Linux, macOS, Windows, FreeBSD, OpenBSD, NetBSD, illumos, Solaris |

The repository and install commands for each system are in
[Get started](./get-started). Or let the installer pick the package manager:

```bash
curl -fsSL https://pkgs.orion-belt.dev/install.sh | sh                 # tools
curl -fsSL https://pkgs.orion-belt.dev/install.sh | sh -s -- agent     # agent
curl -fsSL https://pkgs.orion-belt.dev/install.sh | sh -s -- server    # server
```

On Linux it adds the signed repository (apt, dnf, zypper, apk or pacman) and
installs the package; elsewhere it installs the release binaries in
`/usr/local/bin`. On Windows: `irm https://pkgs.orion-belt.dev/install.ps1 | iex`.

For air-gapped installs, build the packages from a checkout with
`make packages` and install them from `dist/` (see the main repository's
`docs/PACKAGING.md`).

## One-command server install

```bash
curl -fsSL https://raw.githubusercontent.com/orion-belt-dev/orion-belt/master/scripts/install-server.sh | sudo bash
```

Distro-aware: installs deb/rpm/apk when available (else the release binary),
writes `/etc/orion-belt/server.yaml` with your **public URL**, enables systemd
or OpenRC, and runs the setup wizard (admin SSH key — file, paste, or generate).
Can also install local PostgreSQL (`--install-postgres` / interactive choice).

Unattended:

```bash
curl -fsSL https://raw.githubusercontent.com/orion-belt-dev/orion-belt/master/scripts/install-server.sh \
  | sudo bash -s -- --unattended \
  --public-url https://orion.example.com \
  --install-postgres \
  --jwt-secret "$(openssl rand -hex 32)" \
  --admin-email admin@example.com \
  --admin-key-file /root/admin.pub
```

(`--install-postgres` installs/starts local Postgres and creates the `orionbelt`
DB; or pass `--db-url` instead.)

Uninstall (asks separately whether to keep the DB, logs, and recordings):

```bash
sudo bash scripts/install-server.sh --uninstall
# unattended:
sudo bash scripts/install-server.sh --uninstall --unattended --drop-db --drop-logs --drop-data
```

Then continue with [Server setup](./server) / [Agent](./agent).

## Docker

```bash
git clone https://github.com/orion-belt-dev/orion-belt.git
cd orion-belt
./scripts/docker-quickstart.sh
```

The script asks whether to **build from this checkout** or **pull published GHCR
images**. Non-interactive:

```bash
./scripts/docker-quickstart.sh --images        # ghcr.io/orion-belt-dev/...:latest
./scripts/docker-quickstart.sh --from-source   # build Dockerfiles here
```

Full walkthrough: [Try it with Docker](./try-in-10-minutes). For a server on Docker without cloning, see the **Docker** tab in [Get started](./get-started).

### Published images (GHCR)

```bash
docker pull ghcr.io/orion-belt-dev/orion-belt-server:1.4.0
docker pull ghcr.io/orion-belt-dev/orion-belt-agent:1.4.0
# or :latest
```

From a clone: `cp .env.prod.example .env.prod` (set secrets + public URL), then
`make docker-prod-up`. Pin with `ORION_IMAGE_TAG=1.4.0` if you do not want
`:latest`. Image list: [CONTAINERS](https://pkgs.orion-belt.dev/CONTAINERS).

## From source

For development, or a platform without a package. Requires Go **1.27.3+** (see `go.mod`) and Node 22 for the web console.

```bash
git clone https://github.com/orion-belt-dev/orion-belt.git
cd orion-belt
make build
# or: make build-server / build-client / build-agent
```

## Verify

```bash
orion-belt-server --version
curl -s http://localhost:8080/api/v1/version
curl -s http://localhost:8080/api/v1/gateway-info   # advertised public URL / SSH host
```

## Next steps

1. [Get started in 10 minutes](./get-started)
2. [Server setup](./server) for HTTPS, MFA and recording options
3. [Agent setup](./agent) and [Client setup](./client)
