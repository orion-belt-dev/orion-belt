---
slug: rebac-for-ssh-machines
title: Relationship-based access control (ReBAC) for SSH machines
description: How ReBAC maps users to SSH machines and remote users in a self-hosted PAM gateway, when to use OpenFGA, and how Orion Belt keeps grants practical.
authors: [orion]
tags: [pam, architecture, security]
keywords: [ReBAC SSH, OpenFGA, privileged access, self-hosted PAM, SSH authorization]
---

Most SSH fleets still authorize with a pile of `authorized_keys` files and a spreadsheet nobody updates. **Relationship-based access control (ReBAC)** is a clearer model for a **self-hosted PAM** gateway: users have relationships to machines (and to remote OS users), not a single “ops can root everywhere” bit.

{/* truncate */}

This guide explains how ReBAC shows up in Orion Belt, how it differs from coarse RBAC, and when optional **OpenFGA** is worth introducing.

## RBAC vs ReBAC in one paragraph

**RBAC** says: Alice has role `sre`, and role `sre` may access production.  
**ReBAC** says: Alice is `can_access` on machine `web-01` as remote user `deploy` — a direct relationship in a graph.

RBAC scales via roles; ReBAC scales via relationships. SSH privileged access is naturally relational: *person ↔ machine ↔ unix account ↔ time window*.

## How Orion Belt models it

At a practical level:

1. **Users** authenticate with SSH public keys (plus optional MFA on the console).  
2. **Agents / machines** enroll with the gateway.  
3. **Grants** attach a user to a machine with allowed **remote users** (`root`, `ubuntu`, `deploy`, …).  
4. JIT requests create **time-bounded** relationships instead of standing grants.

UI surfaces: **Users**, **Permissions**, **Agents**. CLI examples live in [User management](/docs/guides/user-management) and the server setup docs.

```bash
orion-belt-server permission grant \
  --user alice --machine web-01 --type both --remote-users deploy
```

## Remote users are still Linux users

ReBAC does not invent OS accounts. If you grant `alice → web-01 as root`, the `root` account must exist on the agent host. Orion brokers the session; the agent host still enforces local account reality.

This is intentional. A PAM gateway should not pretend to replace `/etc/passwd`.

## Optional OpenFGA

Some organizations already run [OpenFGA](https://openfga.dev/) (or similar) as a central authorization service. Orion Belt can optionally delegate authorization checks there instead of only using built-in tables.

**Use built-in ReBAC when:**

- You want the smallest self-hosted footprint  
- Your grant graph is “users ↔ machines”  

**Consider OpenFGA when:**

- Authorization is already centralized for other apps  
- You need richer tuple models shared outside SSH  

Misconfiguring external authz is a footgun — fail closed, and test grants in a lab first.

## Least privilege patterns that work

1. **Default deny** — no grant, no SSH through the gateway.  
2. **Per-machine grants** instead of “production” mega-roles when possible.  
3. **Non-root remote users** for day-to-day work; root via JIT.  
4. **Separate auditor identities** for session playback.  
5. **Revoke on offboarding** as a checklist item next to GitHub org removal.

## FAQ

**Is this Zanzibar?**  
ReBAC as a family includes Zanzibar-style systems. Orion’s built-in path is the pragmatic SSH subset; OpenFGA is the escape hatch for fuller models.

**Can groups map to many machines?**  
Use tags/metadata for organization, but remember: **tags are not grants**. Access still needs an explicit permission edge (or a higher-level automation that creates those edges).

## Related

- [JIT SSH access](/blog/jit-access-approvals)  
- [Machine management](/docs/guides/machine-management)  
- [Orion Belt vs Teleport](/blog/orion-belt-vs-teleport)
