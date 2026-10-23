---
slug: looking-for-early-operators
title: Join Orion Belt early operators — first self-hosted SSH PAM deployments
description: We are looking for the first ~20 teams and labs running Orion Belt v1.0. Get install help, influence the roadmap, and help shape a self-hosted Teleport alternative for SSH.
authors: [orion]
tags: [self-hosted, ops, pam]
keywords: [self-hosted PAM community, SSH bastion early access, Orion Belt operators]
---

GitHub stars are a lagging indicator. The leading indicator we care about is simple: **are serious people deploying Orion Belt and telling us what breaks?**

{/* truncate */}

This is an open call for **early operators** — roughly the first twenty labs or small teams running v1.0 as a real **self-hosted SSH PAM / bastion** path.

## Who we want

- Homelab / self-hosting users with more than one Linux box  
- Small company SRE/DevOps teams needing recording + JIT without SaaS  
- Security engineers evaluating a **Teleport alternative** for SSH-only scope  
- People willing to file reproducible bugs (Docker quickstart welcome)

## Who we are not optimizing for (yet)

- Enterprises needing HA + OIDC + multi-protocol on day one  
- Teams blocked by Commons Clause / non-OSI policy ([license notes](/blog/commons-clause-honesty))  
- Drive-by “please add my niche protocol” without deploying SSH first  

## What you get

- Direct help on [Discord](https://discord.gg/w62S8jxTHJ) when agents will not dial out  
- Faster eyes on your issues and PRs  
- Input on roadmap sequencing (OIDC, HA, etc.)  
- Optional public credit as an early operator  

## What we ask

1. Complete [Try in 10 minutes](/docs/quickstart/try-in-10-minutes)  
2. Leave it running on something you actually use (even a homelab VM)  
3. Open GitHub issues with versions, logs, and repro steps  
4. Tell us if docs lied  

## How to raise your hand

- Message in Discord  
- Open a [GitHub Discussion](https://github.com/orion-belt-dev/orion-belt/discussions)  
- Or comment on an issue that you are running v1.0 in anger  

Metrics we track privately for the first month: install attempts, active deployments, external issues — not vanity follower counts. Background: [lessons building PAM](/blog/lessons-building-self-hosted-pam).

## Related

- [v1.0 announcement](/blog/welcome-orion-belt-v1)  
- [Why reverse SSH agents](/blog/why-reverse-ssh-agents)
