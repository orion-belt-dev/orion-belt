---
slug: self-service-ssh-access-requests
title: Self-service SSH access requests — let engineers see what they can ask for
description: Just-in-time access only works if people know what to request. Orion Belt 1.2.1 adds an access catalog that shows each user what they can request, plus authentication and proxy hardening and a terminal resize fix.
date: 2026-09-30T12:00:00Z
authors: [orion]
tags: [pam, ssh, security]
keywords: [self-service access request, SSH access request workflow, just-in-time SSH access, access catalog, Orion Belt 1.2.1]
---

**Just-in-time access fails quietly when engineers don't know what to ask
for.** They guess machine names, request too broadly "to be safe", or ping an
admin on chat, which is the workflow JIT was supposed to replace.
**Orion Belt 1.2.1**, released September 29, adds an access catalog to fix
that.

{/* truncate */}

## The access catalog

The console now shows each user the capabilities they could request, based
on their current relationships: which machines, as which remote users, with
which access types. Instead of typing `web-03` and hoping, they pick from a
list that only contains things an approver could actually grant them.

That has a few effects:

- **Fewer rejected requests.** Requests for machines a user has no path to
  don't get filed.
- **Narrower requests.** When the exact option is visible, people stop asking
  for more than they need.
- **No information leak.** The catalog is computed per user, from the same
  relationships the authorization check uses
  ([ReBAC](/blog/rebac-for-ssh-machines)), so it doesn't reveal machines a
  user has no business knowing about.

## How a request flows

1. The engineer picks a target from the catalog, adds a reason and a
   duration, and submits. Or from the CLI:
   `osh --request-access deploy@web-03 --reason "INC-1234" --duration 1800`,
   then `osh requests list` to follow it.
2. Approvers are notified in the console bell and, with the chat plugins, in
   Slack, Discord, Microsoft Teams or Rocket.Chat, where they can approve in
   place.
3. On approval the grant is active for the requested duration, then expires
   without anyone revoking it.
4. The session is recorded and tied to the request in the audit log.

Detail on the approval side: [JIT access approvals](/blog/jit-access-approvals).

## Also in 1.2.1

- **Authentication, authorization and proxy hardening** across the gateway
  (PR #99).
- **Terminal resize fix**: window size changes now reach the target's PTY, so
  full-screen programs (`htop`, `vim`, `less`) redraw correctly when you
  resize your terminal (#103).
- **Tracing fix**: the OTLP/HTTP traces path is set explicitly, so HTTP
  collectors receive spans (PR #101).
- Dependency updates, including gRPC and the OpenTelemetry SDK.

## Designing a request policy that people follow

A catalog helps; the policy around it matters more.

| Decision | Suggested default |
| --- | --- |
| What needs a request | Production and anything holding customer data |
| What stays standing | Dev and staging, for the team that owns them |
| Default duration | 1 hour; 8 hours maximum without a second approver |
| Reason field | Required; link a ticket or incident |
| Approvers | Not the requester; two people for the most sensitive systems |

Keep the request faster than the workaround. If requesting access takes
longer than finding a shared key in someone's dotfiles, people will find the
key.

## FAQ

**What is a self-service access request?**
A request a user files themselves for specific, time-limited access, which an
approver accepts or rejects, instead of asking an admin to change permissions
by hand.

**Can approvals happen in Slack?**
Yes. The chat plugins post requests to Slack, Discord, Teams or Rocket.Chat
with approve and reject actions.

Release notes:
[v1.2.1](https://github.com/orion-belt-dev/orion-belt/releases/tag/v1.2.1)
