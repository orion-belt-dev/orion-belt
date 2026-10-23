---
sidebar_position: 1
title: Installation
---

# Installation

Install Orion Belt via Docker (fastest), curl|bash on Linux, packages, or from source.

:::tip Fastest path
For a lab that ends in a recorded session, follow **[Try in 10 minutes](./try-in-10-minutes)** (`./scripts/docker-quickstart.sh`).
:::

## System requirements

| Component | CPU | RAM | Disk | OS |
| --- | --- | --- | --- | --- |
| Server | 2+ cores | 4GB+ | 20GB+ | Linux (Ubuntu 20.04+, RHEL 8+, …) |
| Agent | 1+ core | 512MB+ | 10GB+ | Linux (any modern distro) |
| Client | Any | 256MB | 100MB | Linux, macOS, Windows |

## Docker (recommended to try)

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

Full walkthrough: [Try in 10 minutes](./try-in-10-minutes).

### Published images (GHCR)

```bash
docker pull ghcr.io/orion-belt-dev/orion-belt-server:1.2.0
docker pull ghcr.io/orion-belt-dev/orion-belt-agent:1.2.0
# or :latest
```

From a clone: `cp .env.prod.example .env.prod` (set secrets + public URL), then
`make docker-prod-up`. Pin with `ORION_IMAGE_TAG=1.2.0` if you do not want
`:latest`. Image list: [packages/CONTAINERS](https://orion-belt-dev.github.io/packages/CONTAINERS).

## curl | bash (Linux server)

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

## From source

Requires Go **1.26.5+** (see `go.mod`).

```bash
git clone https://github.com/orion-belt-dev/orion-belt.git
cd orion-belt
make build
# or: make build-server / build-client / build-agent
```

## Packages (deb / rpm / apk)

Public mirror (default for **Add agent** install scripts):

```text
https://orion-belt-dev.github.io/packages
```

From a checkout of the repo (local / air-gapped builds):

```bash
make packages
# install from dist/ — see the main repo docs/PACKAGING.md
```

After package install, set `server.public_url` (and optional `public_ssh_host` /
`public_ssh_port`), run the setup wizard, and add agents — see
[Server setup](./server) and [Agent](./agent).

## Verify

```bash
orion-belt-server --version
curl -s http://localhost:8080/api/v1/version
curl -s http://localhost:8080/api/v1/gateway-info   # advertised public URL / SSH host
```

## Next steps

1. [Server setup](./server)
2. [Agent setup](./agent)
3. [Client / first connection](./client)
