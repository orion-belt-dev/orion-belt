---
slug: bastion-vs-pam
title: SSH bastion host vs PAM gateway — which problem are you solving?
description: Clarify bastion hosts versus privileged access management (PAM) for SSH. When a jump box is enough, when you need recording and JIT, and where Orion Belt fits.
authors: [orion]
tags: [ssh, pam, architecture]
keywords: [SSH bastion host, PAM vs bastion, privileged access management SSH, self-hosted bastion]
---

Engineers say “bastion” to mean three different products. That ambiguity wastes evaluation time. This article separates **SSH bastion hosts** from **PAM gateways**, and places Orion Belt on that map.

{/* truncate */}

## Definitions that help

### Jump host / classic bastion

A hardened server you SSH into, then pivot to internal hosts. Network rendezvous. Policy and audit are usually DIY.

### SSH access gateway / recording bastion

A control plane that brokers SSH, often with session recording and centralized authn. May use reverse tunnels or agents.

### Privileged access management (PAM)

A broader discipline: joiner-mover-leaver, credential vaults, password rotation, multi-protocol privileged sessions, tickets, and compliance reports. SSH is one slice.

## Decision table

| Need | Jump host | SSH gateway | Full PAM suite |
| --- | --- | --- | --- |
| Reach internal SSH | ✓ | ✓ | ✓ |
| Per-machine grants | ✗ / DIY | ✓ | ✓ |
| Session recording | ✗ / DIY | ✓ | ✓ |
| JIT approvals | ✗ | ✓ / varies | ✓ |
| K8s/DB/Windows too | ✗ | ✗ / limited | ✓ |
| Lowest ops cost | ✓ | Medium | High |

## Where Orion Belt fits

Orion Belt is a **self-hosted SSH PAM gateway** (bastion + PAM workflows for SSH):

- Reverse agents (dial out)  
- Recording + live watch  
- JIT + MFA + ReBAC  
- Not a full multi-protocol PAM suite in v1  

If your RFP says “PAM” but the checklist is SSH + audit + approvals, a focused gateway may close the RFP without a platform migration. If the checklist includes password checkout for Windows and database proxying next quarter, shortlist broader tools — see [open-source PAM landscape](/blog/open-source-pam-landscape) and [vs Teleport](/blog/orion-belt-vs-teleport).

## Migration pattern we see

1. Today: jump host + VPN  
2. Pain: audits, shared keys, inbound SSH  
3. Next: SSH gateway with recording (Orion Belt class)  
4. Later: expand protocols or IdP integration as needed (OIDC on our roadmap)

Do not buy step 4 on day one unless step 4’s problems are already on fire.

## Related

- [Why reverse SSH agents](/blog/why-reverse-ssh-agents)  
- [Announcing Orion Belt](/blog/announcing-orion-belt)
