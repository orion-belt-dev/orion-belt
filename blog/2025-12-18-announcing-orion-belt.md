---
slug: announcing-orion-belt
title: Announcing Orion Belt — open-source self-hosted SSH PAM / bastion
description: Orion Belt is a self-hosted SSH access gateway with PAM workflows — reverse agents, session recording, JIT approvals, MFA, and ReBAC. A lighter Teleport alternative for SSH-first teams.
authors: [orion]
tags: [ssh, pam, self-hosted]
keywords: [open source PAM, SSH bastion, self-hosted privileged access, Teleport alternative]
---

**Orion Belt** is an open-source, **self-hosted SSH access gateway** with PAM-style workflows. It sits in the gap between a raw jump host and a full zero-trust platform: reverse agents, session recording, live watch, JIT approvals, MFA/WebAuthn, ReBAC, and an optional SSH CA — without SaaS dependency.

{/* truncate */}

This post is the product introduction for operators evaluating an **open source PAM** option focused on SSH, or a practical **Teleport alternative** when you do not need every protocol on day one.

## Who this is for

- DevOps / SRE / Linux admins managing fleets over SSH  
- Homelab and **self-hosted** users who want audit trails without a vendor cloud  
- Small companies that need **privileged access** controls but not a CyberArk deployment  
- Teams tired of inbound `:22` on every box “just for the bastion”

## The problem we kept hitting

Traditional SSH access fails in predictable ways:

- Long-lived keys copied into chat  
- No native approval workflow for break-glass  
- Spotty or nonexistent session recording  
- Broad VPN access instead of per-machine grants  
- “Enterprise PAM” locked behind proprietary licensing and SaaS

Commercial products and large open platforms solve real problems — and often arrive with an ops surface larger than the SSH problem you actually have.

## What Orion Belt is (and is not)

### It is

A **Linux-first SSH/SCP bastion gateway** you run yourself:

| Capability | Role |
| --- | --- |
| Reverse agents | Targets dial **out** — no inbound hole required on the host for Orion |
| Gateway | Brokers sessions, enforces ReBAC, records traffic |
| JIT access | Request → approve → expire (UI / API / ChatOps) |
| MFA | TOTP + WebAuthn for the console; FIDO SSH keys supported |
| Clients | `osh` / `ocp` / `oadmin`, vanilla OpenSSH, or web terminal |
| Packaging | Docker quickstart, deb/rpm/apk |

### It is not

- A multi-protocol mesh for Kubernetes, Windows RDP, and databases on day one (roadmap items exist; SSH is the v1 promise)  
- A hosted SaaS (by design)  
- A drop-in replacement for every Teleport feature  

Positioning we use deliberately: *Teleport-like SSH access control without opening inbound ports or adopting a large platform.*

## Architecture in one diagram

```text
CLI / OpenSSH / Web UI  →  Gateway (:2222 SSH, :8080 UI)
                              ↑ reverse SSH
                           Agent on target
```

Details: [Architecture overview](/docs/architecture/overview) and [Why reverse SSH agents](/blog/why-reverse-ssh-agents).

## License — said up front

Orion Belt ships under **Apache 2.0 + Commons Clause**:

- Free to use, modify, and **self-host**, including internal commercial use  
- You **cannot sell** Orion Belt (or a hosted service whose value derives substantially from it) as a product  

We disclose this on community posts (HN, Lobsters, Reddit) because security software requires trust. Full text: [LICENSE](https://github.com/orion-belt-dev/orion-belt/blob/master/LICENSE). More context: [Commons Clause honesty](/blog/commons-clause-honesty).

## Try it

```bash
git clone https://github.com/orion-belt-dev/orion-belt.git
cd orion-belt
./scripts/docker-quickstart.sh
```

Walkthrough: [Try Orion Belt in 10 minutes](/docs/quickstart/try-in-10-minutes).

## Links

- Website: [orion-belt.dev](https://orion-belt.dev)  
- Source: [github.com/orion-belt-dev/orion-belt](https://github.com/orion-belt-dev/orion-belt)  
- Chat: [Discord](https://discord.gg/w62S8jxTHJ)  
- Compare: [Orion Belt vs Teleport](/blog/orion-belt-vs-teleport)
