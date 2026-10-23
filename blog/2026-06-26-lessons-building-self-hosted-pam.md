---
slug: lessons-building-self-hosted-pam
title: Lessons learned building a self-hosted SSH PAM gateway
description: Practical lessons from building Orion Belt — access control vs audit, UX for JIT, reverse tunnel tradeoffs, licensing honesty, and keeping a 10-minute install path sacred.
authors: [orion]
tags: [pam, architecture, ops]
keywords: [self-hosted PAM, building SSH bastion, privileged access lessons, open source PAM]
---

Orion Belt v1.0 is our line in the sand for a stable **self-hosted SSH PAM** slice. These are the lessons that shaped it — useful if you are building similar infrastructure or evaluating whether our tradeoffs match yours.

{/* truncate */}

## 1. Access control is easy compared to audit

ReBAC graphs are approachable. Faithful, replayable, retainable **session recordings** are not. Budget engineering time accordingly. Deep dive: [Session recording is hard](/blog/session-recording-is-hard).

## 2. UX beats feature count for JIT

If temporary access takes longer than sharing a key, standing keys win. ChatOps approvals and a short CLI/UI path matter more than a twelfth policy DSL. See [JIT access](/blog/jit-access-approvals).

## 3. Reverse tunnels move risk; they don’t delete it

Agents dialing out remove inbound SSH on targets and concentrate trust on the gateway. Harden the gateway like a directory service. Explained in [Why reverse SSH agents](/blog/why-reverse-ssh-agents).

## 4. Optional complexity must stay optional

SSH CA, OpenFGA, and WebAuthn are valuable. None of them should block the first recorded session. Optional flags exist so labs stay fast ([SSH CA](/blog/optional-ssh-ca), [MFA](/blog/mfa-webauthn-ssh)).

## 5. License clarity is a security feature

People evaluating PAM software are professionally paranoid. Commons Clause disclosed early builds trust even when it loses some adopters. [License FAQ](/blog/commons-clause-honesty).

## 6. Protect the 10-minute path like an API

Every feature PR should ask: did we break `docker-quickstart` → agent → Sessions? If yes, fix or document before merge. [Install path design](/blog/ten-minute-install-path).

## 7. Say no to protocol sprawl in v1

Kubernetes and database proxies are real needs. Shipping a mediocre version of each would dilute the SSH promise. Roadmap honesty > checkbox theater. Comparison mindset: [vs Teleport](/blog/orion-belt-vs-teleport).

## 8. Dual packaging is worth it

Docker converts; packages retain. [Packages and labs](/blog/linux-packages-and-labs).

## What we want next from the community

Sharp issues from people who **deployed** something — not drive-by feature wishes. If that is you: [early operators](/blog/looking-for-early-operators).

## Related

- [Announcing Orion Belt](/blog/announcing-orion-belt)  
- [Discord](https://discord.gg/w62S8jxTHJ) · [GitHub](https://github.com/orion-belt-dev/orion-belt)
