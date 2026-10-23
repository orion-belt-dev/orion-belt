---
slug: jit-access-approvals
title: Just-in-time (JIT) SSH access with approvals that people will use
description: How to design JIT privileged access for SSH — request, approve, expire — with ChatOps and session recording. Orion Belt’s approach to temporary access workflows.
authors: [orion]
tags: [pam, ops, security]
keywords: [JIT access, temporary privileged access, SSH approval workflow, self-hosted PAM]
---

Standing SSH privileges are convenient until an ex-contractor key still works on Friday night. **Just-in-time (JIT) access** — request, approve, expire — is the standard answer in privileged access management. It only works if the workflow is faster than pasting a key into Slack.

{/* truncate */}

This article covers how JIT fits a **self-hosted SSH PAM** gateway, what UX details matter, and how Orion Belt implements temporary access without making on-call hate you.

## Why standing access fails

- Keys outlive tickets  
- “Emergency” grants become permanent  
- Audit asks “who had root last month?” and the answer is “everyone in `#ops`”  
- Compliance frameworks increasingly expect **time-bound** privileged sessions  

Commercial PAM vendors built empires on this pain. Open-source and self-hosted teams still under-serve it.

## Anatomy of a usable JIT flow

A flow people will follow:

1. Engineer needs `web-01` for 90 minutes  
2. They submit a **reason + duration** (CLI or UI)  
3. Approver gets a ping (console bell and/or ChatOps)  
4. Approval creates a grant that **auto-expires**  
5. Session is **recorded** while they work  
6. Grant disappears; recording remains for review  

A flow people will bypass:

1. Open four tabs of wiki  
2. File a ticket in a system nobody watches at 2am  
3. Wait for a manager who is asleep  
4. …so they reuse yesterday’s shared key  

## Orion Belt’s building blocks

- **Access requests** with reason and duration  
- **Approvals** in the web console  
- **ChatOps plugins** for Slack, Discord, Teams, Rocket.Chat — approvals where the team already lives  
- **Recording + live watch** on the gateway path  
- ReBAC grants underneath (see [ReBAC for SSH machines](/blog/rebac-for-ssh-machines))  

Example CLI shape:

```bash
osh web-01 --request-access --duration 1h --reason "Incident 4821 rollback"
```

## ChatOps without turning chat into your IdP

Chat approvals are great for speed. They are not a substitute for:

- Authenticating the approver properly  
- Logging who approved what  
- Ensuring the bot cannot be spoofed casually  

Keep ChatOps as a **notification + action surface** bound back to the gateway’s authz, not a free-for-all emoji vote.

## Pair JIT with recording

Temporary access without audit just **delays** standing privilege. The conversion story we optimize for in docs is:

request → approve → SSH → **Sessions playback**

Lab walkthrough: [Try in 10 minutes](/docs/quickstart/try-in-10-minutes).

## Policy ideas you can copy

| Scenario | Duration | Approver | Remote user |
| --- | --- | --- | --- |
| On-call break-glass | 1–2h | Another on-call / lead | root |
| Deploy to one app host | 30–60m | Service owner | deploy |
| Vendor debug | 2–4h | Security + owner | restricted user |
| Homelab tinkering | 24h | You | whatever you use |

## FAQ

**Does JIT replace MFA?**  
No. MFA protects the console / identity; JIT limits *when* privileged relationships exist.

**What if the approver is offline?**  
Define a backup approver rotation. A JIT system with one human bottleneck will be routed around.

## Related

- [Session recording is hard](/blog/session-recording-is-hard)  
- [Looking for early operators](/blog/looking-for-early-operators)  
- [Discord](https://discord.gg/w62S8jxTHJ)
