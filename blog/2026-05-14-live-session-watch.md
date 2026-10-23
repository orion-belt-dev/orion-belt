---
slug: live-session-watch
title: Live SSH session watch for self-hosted PAM gateways
description: How live session watch works on an SSH bastion, privacy and permission concerns, and how Orion Belt lets admins observe active privileged sessions safely.
authors: [orion]
tags: [ssh, security, ops]
keywords: [live SSH session, session monitoring, privileged session watch, SSH bastion audit]
---

Sometimes playback tomorrow is too late. **Live session watch** lets an authorized admin observe an active SSH (or web terminal) session as it happens — useful for incident response, mentoring, and verifying break-glass work.

{/* truncate */}

Used badly, it becomes silent surveillance. Used well, it is a sharp tool in a **self-hosted PAM** gateway. Here is how we think about it in Orion Belt.

## Recording vs live watch

| | Session recording | Live watch |
| --- | --- | --- |
| Timing | Persisted cast for later | Streaming view of an active session |
| Primary use | Compliance, forensics | Real-time oversight |
| Failure mode | Disk secrets / retention | Privacy / overreach |

Both sit on the **gateway path**. Direct-to-host SSH remains invisible. Cultural rule still applies: [session recording is hard](/blog/session-recording-is-hard).

## Product requirements we care about

1. **Explicit action** — an admin opens watch; it is not ambient for every operator  
2. **Authorization** — not every user role can watch every session  
3. **Honest lifecycle** — when the session ends, watch ends  
4. **Same trust boundary** as recording — if you can watch, you are already in the sensitive-data club  

## When to use it

- On-call verifying a risky production change  
- Approver stays online during a JIT window ([JIT access](/blog/jit-access-approvals))  
- Training: senior watches junior on a staging host  
- Suspected account misuse (with clear policy)

## When not to use it

- Routine browsing of everyone’s terminals “because we can”  
- Legal environments without disclosed monitoring policy  
- As a substitute for proper pairing tools when the goal is collaboration, not control  

## Operator tips

- Prefer watching **JIT sessions** you already approved  
- Prefer staging for training watches  
- Assume anything typed can be seen — same as recording  
- Combine with ChatOps approvals so the human story is in one timeline  

Try it after a lab SSH in [Try in 10 minutes](/docs/quickstart/try-in-10-minutes) — open **Sessions** while the session is still active.

## Related

- [Sessions guide](/docs/guides/sessions)  
- [Security FAQ](/docs/faq/security)
