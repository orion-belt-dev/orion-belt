---
slug: ten-minute-install-path
title: How we designed a 10-minute SSH PAM install path
description: Why Orion Belt optimizes for docker-quickstart → agent dial-out → recorded SSH. Building a conversion-friendly self-hosted bastion onboarding experience.
authors: [orion]
tags: [ops, self-hosted, ssh]
keywords: [self-hosted SSH bastion, Docker PAM, quickstart, Orion Belt install]
---

Most infrastructure products document *everything*. Conversion dies on page one. For Orion Belt, the path we care about is short on purpose:

**gateway up → agent dials out → SSH works → session recorded → wow**

{/* truncate */}

This post explains why that sequence matters for a **self-hosted SSH bastion / PAM** product, what we automated in `docker-quickstart.sh`, and what we deliberately left for production docs.

## The funnel that actually matters

Stars and HN traffic are useless if the README dumps people into YAML archaeology. Security tools especially need a **trusted first success**: if the demo lies, nobody will harden a real deployment.

We optimize for:

1. Clone the repo  
2. One script starts Postgres + gateway + admin bootstrap  
3. Browser login with an SSH public key  
4. **Add agent** → outbound connect  
5. Open a session → see it under **Sessions**

Guide: [Try Orion Belt in 10 minutes](/docs/quickstart/try-in-10-minutes).

## What the quickstart script does

`./scripts/docker-quickstart.sh`:

- Writes `.env.server` with random Postgres password + JWT secret if missing  
- `docker compose up` for the server stack  
- Waits for `/health`  
- Generates an admin ed25519 keypair if needed  
- Runs `setup` to create the first admin  

There is **no** self-service signup form. That is intentional: the control plane is SSH-key identity, not a random password form on the internet.

## Why Docker for labs and packages for prod

| Audience | Path |
| --- | --- |
| Evaluator / bug repro | Docker quickstart + compose agent |
| Homelab lasting a weekend | Docker or packages |
| Company gateway | deb/rpm/apk + hardening docs |

Both are real. Collapsing them into one doc produces a bad lab *and* a bad production guide. See [Linux packages and labs](/blog/linux-packages-and-labs).

## Friction we refuse to ship in the first ten minutes

- Manual SQL  
- Hand-editing JWT secrets without a generator  
- Requiring a public DNS name before hello-world  
- Forcing SSH CA on  

Those belong *after* the wow.

## Measure the path

If you fork or contribute, break the 10-minute path on purpose and see what fails. Every failure mode should either be fixed or documented in the troubleshooting table — not discovered on HN launch day.

## Related

- [Why reverse SSH agents](/blog/why-reverse-ssh-agents)  
- [Server setup](/docs/quickstart/server)  
- [GitHub](https://github.com/orion-belt-dev/orion-belt)
