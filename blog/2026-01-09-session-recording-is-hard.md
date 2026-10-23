---
slug: session-recording-is-hard
title: Why SSH session recording is harder than access control
description: Building reliable SSH session recording for a self-hosted PAM gateway — PTY quirks, secret leakage, retention, encryption, and live watch. How Orion Belt approaches auditability.
authors: [orion]
tags: [ssh, pam, security]
keywords: [SSH session recording, privileged access audit, self-hosted PAM, SSH bastion recording]
---

Granting “alice may SSH to `web-01`” is the easy half of privileged access. The hard half is answering, months later: **what exactly happened in that shell?** Access control is a graph. **SSH session recording** is a stream of messy terminal I/O, storage policy, and human behavior.

{/* truncate */}

This article is for operators evaluating **self-hosted PAM** or an **SSH bastion** with audit requirements — SOC-minded startups, regulated teams, and homelabs that still want a paper trail.

## Access control vs auditability

| Concern | Typical difficulty | Failure mode |
| --- | --- | --- |
| Authn (keys, MFA) | Medium | Stolen key, weak MFA |
| Authz (who → which host) | Medium | Over-broad roles |
| **Recording** | High | Gaps, unreadable casts, secret sprawl on disk |
| Live watch | High | Latency, privacy, “who can watch whom” |

Many DIY bastions stop after authz. Then an incident arrives and nobody can replay the session that mattered.

## Why terminal recording is messy

### PTYs are not logs

Interactive SSH is a pseudo-terminal: resize events, ANSI color, cursor motion, binary-ish output from `top` or `vim`. A naive “log stdout” approach produces files humans cannot replay. Orion Belt uses cast-style recordings suitable for console playback — the same family of ideas behind asciinema-like players.

### Secrets show up in cleartext

People paste API tokens into shells. Recordings then become **secret stores**. Treat recording storage like a vault:

- Encrypt at rest when possible (`recording.encryption_key` in Orion)  
- Restrict who can open **Sessions** in the UI  
- Set **retention** deliberately  
- Train operators not to paste production secrets into recorded sessions (or use short-lived creds)

### Completeness depends on the path

If a user SSHs **directly** to the agent host, the gateway never sees the session. Recording is not magic — it only covers traffic that traverses the gateway. The operational rule has to be cultural and technical: **gateway or it didn’t happen.**

## What “good” looks like in a PAM gateway

For Orion Belt’s v1 SSH slice we care about:

1. **Record by default** on the Docker / recommended path  
2. **Playback** in the web console (seek / pause)  
3. **Live watch** for active sessions when an admin needs eyes-on  
4. **Compression + retention** knobs for disk reality  
5. Optional **encryption at rest**

Web terminal sessions are first-class too (`source=web`) — not only CLI SSH.

## Live watch vs recording

| | Recording | Live watch |
| --- | --- | --- |
| When | After / during flush | During active session |
| Use | Incident review, compliance | Pairing, suspicious activity |
| Risk | Secrets on disk | Privacy / over-surveillance |

Both should be **permissioned**. Silent always-on screen sharing for every admin is how you lose trust inside the company.

Deep dive: [Live session watch](/blog/live-session-watch).

## Implementation checklist for self-hosted teams

Before you call your **SSH bastion** “auditable”:

1. Force human SSH through the gateway (OpenSSH `user+host@gw` or `osh`)  
2. Enable recording; set retention days you can defend  
3. Turn on encryption at rest for anything beyond a lab  
4. Limit replay roles (auditor vs operator)  
5. Test a full loop: connect → type → disconnect → playback  

Lab path: [Try in 10 minutes](/docs/quickstart/try-in-10-minutes) ends on the Sessions page for a reason — that is the conversion moment.

## FAQ

**Are recordings a substitute for shell history?**  
No. Histories are per-user, incomplete, and editable. Gateway recordings are a different control.

**Can I disable recording?**  
Yes in config — not recommended if you claimed PAM-style audit to leadership.

**Where are files stored?**  
Default `/var/lib/orion-belt/recordings` (or the Docker volume). Plan disk like you plan log volume.

## Related

- [Why reverse SSH agents](/blog/why-reverse-ssh-agents)  
- [JIT access approvals](/blog/jit-access-approvals)  
- [Sessions guide](/docs/guides/sessions)  
- [GitHub](https://github.com/orion-belt-dev/orion-belt)
