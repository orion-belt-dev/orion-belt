---
sidebar_position: 17
title: Server FAQ
---

# Server FAQ

**What ports does the gateway need?**  
- **2222** — SSH (operators + agents)  
- **8080** — HTTP API + `/ui` (put TLS in front in production)

**Can I run multiple gateways / HA?**  
Not a first-class HA story in v1.0 — one gateway (or your own LB experiments). HA is on the roadmap.

**Where are recordings?**  
Default `/var/lib/orion-belt/recordings` (or the Docker volume). Configure under `recording.*`.

**How do I bootstrap the first admin?**  
`orion-belt-server … setup` or `./scripts/docker-quickstart.sh` — SSH public-key auth, no browser signup.

**TLS?**  
Terminate TLS at a reverse proxy for HTTP; SSH remains on 2222 (or your mapped port).
