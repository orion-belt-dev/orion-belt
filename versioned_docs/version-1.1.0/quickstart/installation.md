---
sidebar_position: 1
title: Installation
---

# Installation

Install Orion Belt via Docker (fastest), packages, or from source.

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

That one command starts the gateway, registers a demo machine, and prints a
sign-in link. Full walkthrough: [Try in 10 minutes](./try-in-10-minutes).

### Published images (GHCR)

For a durable install, pull release images instead of building locally:

```bash
docker pull ghcr.io/orion-belt-dev/orion-belt-server:1.1.0
docker pull ghcr.io/orion-belt-dev/orion-belt-agent:1.1.0
# or :latest
```

From a clone: `cp .env.prod.example .env.prod`, then `make docker-prod-up`
(`docker-compose.prod.yml`). Pin with `ORION_IMAGE_TAG=1.1.0` if you do not want
`:latest`. Image list: [packages/CONTAINERS](https://orion-belt-dev.github.io/packages/CONTAINERS).

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

After package install, run the server setup wizard and add agents (see [Server setup](./server) and [Agent](./agent)).

## Verify

```bash
orion-belt-server --version
# or curl the running gateway:
curl -s http://localhost:8080/api/v1/version
```

## Next steps

1. [Server setup](./server)
2. [Agent setup](./agent)
3. [Client / first connection](./client)
