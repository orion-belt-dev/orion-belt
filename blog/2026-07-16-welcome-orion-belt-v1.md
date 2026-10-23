---
slug: welcome-orion-belt-v1
title: Orion Belt v1.0 — self-hosted SSH PAM gateway is stable
description: Orion Belt v1.0 is released — self-hosted SSH access gateway with reverse agents, session recording, JIT, MFA, and ReBAC. Try the 10-minute path and join early operators.
authors: [orion]
tags: [ssh, pam, self-hosted]
keywords: [Orion Belt v1, self-hosted PAM, SSH bastion release, Teleport alternative]
---

**Orion Belt v1.0** is out: a stable, self-hosted **SSH access gateway with PAM workflows**. Reverse agents, session recording, live watch, JIT approvals, MFA/WebAuthn, ReBAC, optional SSH CA, Linux packages, and a web console — without a SaaS dependency.

{/* truncate */}

This is the release note for operators who have been waiting for a version line we are willing to call production-shaped for the SSH slice.

## What “v1.0” means here

- SSH proxy / bastion path we will not casually break  
- Documented Docker quickstart and package installs  
- Recording + live watch on the gateway path  
- JIT + ChatOps approvals  
- MFA options for the console  
- ReBAC (optional OpenFGA)  
- Optional SSH CA  
- OpenAPI + observability docs in the main repo  

Still on the roadmap (not pretend-done): OIDC, HA, additional protocols. Honesty > checkbox theater — [lessons](/blog/lessons-building-self-hosted-pam).

## Positioning

Teleport-like SSH access control — **without** opening inbound ports on every host or adopting a large platform.

Compare: [Orion Belt vs Teleport](/blog/orion-belt-vs-teleport). Landscape: [open-source PAM guide](/blog/open-source-pam-landscape).

## Try it in about ten minutes

```bash
git clone https://github.com/orion-belt-dev/orion-belt.git
cd orion-belt
./scripts/docker-quickstart.sh
```

Then: [Try Orion Belt in 10 minutes](/docs/quickstart/try-in-10-minutes) through agent → SSH → Sessions playback.

## License

Apache 2.0 + Commons Clause — free to self-host and use internally; you cannot sell Orion Belt as a product or substantially derived hosted service. [Plain-language FAQ](/blog/commons-clause-honesty).

## Join early operators

We want deployments and sharp feedback more than vanity metrics. [Early operators call](/blog/looking-for-early-operators) · [Discord](https://discord.gg/w62S8jxTHJ) · [Discussions](https://github.com/orion-belt-dev/orion-belt/discussions)

## Links

- [orion-belt.dev](https://orion-belt.dev)  
- [GitHub](https://github.com/orion-belt-dev/orion-belt)  
- [Documentation](/docs/introduction)
