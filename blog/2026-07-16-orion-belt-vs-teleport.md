---
slug: orion-belt-vs-teleport
title: Orion Belt vs Teleport — a practical Teleport alternative for SSH PAM
description: Compare Orion Belt and Teleport for self-hosted privileged access. When a lighter SSH bastion/PAM gateway wins, when Teleport’s broader platform is worth it, plus Boundary and classic bastions.
authors: [orion]
tags: [ssh, pam, teleport, self-hosted]
keywords: [Teleport alternative, Orion Belt vs Teleport, open source PAM, self-hosted SSH bastion, Boundary vs bastion]
---

**Short version:** Orion Belt is a self-hosted **SSH access gateway with PAM workflows**. Teleport is a **broad zero-trust access platform**. If you mainly need mediated SSH, recording, JIT, and MFA — without inbound ports on every host or a large ops surface — Orion Belt is the lighter **Teleport alternative**.

{/* truncate */}

This comparison is for practitioners evaluating **open source PAM** / **self-hosted privileged access** options for SSH, not a dunk contest. Licensing differs; verify current Teleport terms for your use case. Orion Belt is Apache 2.0 + Commons Clause ([details](/blog/commons-clause-honesty)).

## Who should read this

- Teams searching for a **Teleport alternative** focused on SSH  
- Homelab and SMB operators who find Teleport’s surface area large  
- Security engineers comparing bastion vs platform approaches  

## Side-by-side

| Dimension | Orion Belt | Teleport | HashiCorp Boundary | Traditional bastion |
| --- | --- | --- | --- | --- |
| Primary job | SSH PAM / bastion gateway | Multi-protocol zero-trust access | Identity-aware session brokering | Network jump point |
| Protocols (typical) | SSH / SCP (focused) | SSH, K8s, DB, web, Windows, … | Many via targets/workers | Whatever you tunnel |
| How targets are reached | **Agents dial out** (reverse SSH) | Node agents / tunnels / IoT | Workers / proxies | Inbound to bastion; often inbound to hosts |
| Inbound on targets | Not required for agent path | Depends on mode | Depends on layout | Common |
| Session recording | Yes (+ live watch) | Yes | Yes (with recording path) | DIY |
| JIT / approvals | Built-in (+ ChatOps) | Access requests / roles | Via IdP / workflows | Rare |
| MFA | TOTP + WebAuthn; FIDO SSH keys | Strong IdP / MFA story | Via IdP | DIY |
| Authorization | ReBAC (+ optional OpenFGA) | RBAC / roles / traits | IAM-centric | OS users / keys |
| SSH CA | Optional built-in | First-class cert story | Different model | Manual / none |
| Ops weight | Smaller SSH-focused deploy | Larger platform | Medium–large | Small but feature-poor |
| Hosting | Self-hosted | Self-hosted or cloud | Self-hosted or HCP | Self-hosted |

## Architecture contrast

### Traditional bastion

```text
Operator → (SSH) → Bastion → (SSH) → Target
```

You still manage keys, often lack recording/JIT, and frequently still expose SSH somewhere on the path.

### Orion Belt

```text
Operator → Gateway ← (reverse SSH) ← Agent on target
```

The target initiates the tunnel. The gateway brokers sessions, enforces policy, and can record or live-watch. Background: [Why reverse SSH agents](/blog/why-reverse-ssh-agents).

### Teleport-shaped platforms

Powerful and broad: many protocols, deep identity integration, large feature surface — and correspondingly more to deploy, learn, and operate.

## When to choose Orion Belt

Choose Orion Belt if most of these are true:

- Privileged access is **mostly SSH**  
- You want **no inbound SSH** on application hosts  
- You want **recording + live watch + JIT** without a PAM appliance  
- You prefer **self-hosted**, Linux-first, inspectable code  
- You accept a **narrower scope** today (OIDC, HA, other protocols on the roadmap)

Hands-on: [Try in 10 minutes](/docs/quickstart/try-in-10-minutes).

## When Teleport (or similar) is a better fit

Choose Teleport if you need:

- First-class **Kubernetes / DB / Windows / web** access in one product now  
- A large ecosystem and org-wide zero-trust program already centered on that tool  
- Features Orion Belt has not shipped yet  

## When Boundary fits better

Boundary shines when the center of gravity is **identity + credential brokering** in a HashiCorp world — not “SSH bastion with PAM workflows” as the primary UX.

## Security tradeoffs (reverse agents)

**Gain:** targets need outbound reachability to the gateway, not inbound SSH from attackers’ scan range.  
**Cost:** offline agents are unreachable; the gateway is a high-value control plane — harden it, encrypt recordings, require MFA for admins.

## SEO-style FAQ

**Is Orion Belt a Teleport alternative?**  
For SSH-centric self-hosted PAM/bastion needs, yes. For full multi-protocol zero trust, Teleport remains broader.

**Is Orion Belt open source?**  
Source-available under Apache 2.0 + Commons Clause — free to self-host; not for sale as a product/SaaS wrapper. Not OSI “Open Source” in the strict sense.

**Can I migrate from a jump host?**  
Yes: enroll agents, move humans to `user+host@gateway`, turn on recording, then tighten inbound SSH. See [bastion vs PAM](/blog/bastion-vs-pam).

## Try before you debate

```bash
git clone https://github.com/orion-belt-dev/orion-belt.git
cd orion-belt
./scripts/docker-quickstart.sh
```

[Documentation](/docs/introduction) · [GitHub](https://github.com/orion-belt-dev/orion-belt) · [Discord](https://discord.gg/w62S8jxTHJ)
