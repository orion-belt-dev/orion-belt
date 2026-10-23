---
slug: open-source-pam-landscape
title: Open-source PAM and SSH bastion options compared
description: A practical guide to open-source privileged access and SSH bastions — Teleport, Boundary, Warpgate, DIY jump hosts, and Orion Belt as a self-hosted SSH-focused PAM gateway.
authors: [orion]
tags: [pam, teleport, self-hosted, ssh]
keywords: [open source PAM, Teleport alternative, SSH bastion, self-hosted privileged access, Warpgate, Boundary]
---

Search for **open source PAM** or **Teleport alternative** and you will find overlapping tools that solve different jobs. This guide maps the landscape for SSH-first teams: what each category is good at, where it gets heavy, and where **Orion Belt** fits as a **self-hosted SSH bastion / PAM gateway**.

{/* truncate */}

## Categories that get conflated

### 1. Jump hosts / classic bastions

A hardened box you SSH into, then hop onward. Cheap to start. Weak on recording, JIT, and per-machine policy unless you invent them.

### 2. SSH gateways / recording proxies

Tools that terminate or broker SSH and may record sessions (Warpgate-like products, custom `sshd` ForceCommand setups, etc.). Stronger audit story; feature depth varies.

### 3. Identity-centric brokers

HashiCorp **Boundary** emphasizes identity and credential brokering in a Hashi ecosystem. Excellent if that is already your center of gravity; different UX if you mainly wanted an SSH bastion.

### 4. Broad zero-trust platforms

**Teleport** (and similar) cover SSH plus Kubernetes, databases, web apps, Windows, and a large policy surface. Ideal when you need that breadth; heavy when you only needed SSH PAM.

### 5. Commercial PAM appliances

CyberArk / BeyondTrust class: deep enterprise workflows, price and complexity to match.

## Comparison table

| Option | Best at | Watch-outs |
| --- | --- | --- |
| DIY jump host | Tiny labs | No JIT/recording by default |
| Warpgate-style gateway | Simple shared SSH gateway | PAM workflows may be thinner |
| Boundary | Identity + Hashi stack | Not “SSH bastion UX” first |
| Teleport | Multi-protocol zero trust | Ops and learning curve |
| Orion Belt | Self-hosted **SSH PAM** slice, reverse agents | Narrower protocol scope in v1 |
| Commercial PAM | Enterprise process depth | Cost, lock-in |

## Where Orion Belt sits

Orion Belt optimizes for:

- **Self-hosted privileged access** focused on SSH/SCP  
- **Agents dial out** (no inbound SSH required on targets)  
- Session **recording + live watch**  
- **JIT approvals** (+ ChatOps)  
- MFA / WebAuthn, ReBAC, optional SSH CA  
- Linux packages + Docker quickstart  

It is intentionally a **lighter Teleport alternative** for teams whose privileged access problem is mostly SSH — not a claim to replace every Teleport protocol.

Deep dive: [Orion Belt vs Teleport](/blog/orion-belt-vs-teleport). Architecture: [Why reverse SSH agents](/blog/why-reverse-ssh-agents).

## How to choose in one meeting

Ask:

1. Do we need **non-SSH protocols** this quarter?  
2. Must targets stay **free of inbound SSH**?  
3. Is **session recording** a hard requirement?  
4. Do we need **JIT approvals** or is standing access OK?  
5. Can we run a gateway (Postgres, backups) ourselves?  
6. Does license policy allow **Apache 2.0 + Commons Clause**?

If answers skew toward SSH + self-host + recording/JIT, evaluate Orion Belt hands-on:

[Try in 10 minutes](/docs/quickstart/try-in-10-minutes)

## Related reading

- [Announcing Orion Belt](/blog/announcing-orion-belt)  
- [Bastion vs PAM](/blog/bastion-vs-pam)  
- [Commons Clause honesty](/blog/commons-clause-honesty)
