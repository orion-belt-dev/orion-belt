---
slug: what-is-privileged-access-management
title: What is privileged access management (PAM)? A plain-English guide for infrastructure teams
description: Privileged access management (PAM) controls, approves, records and expires access to the accounts and systems that can do the most damage. What PAM covers, the terms (PASM, PEDM, JIT, standing privilege), and how it applies to SSH.
authors: [orion]
tags: [pam, security, ssh]
keywords: [what is PAM, privileged access management, PAM meaning, PASM, PEDM, just-in-time access, standing privilege, SSH PAM]
---

**Privileged access management (PAM) is the set of controls that decides
who may use powerful accounts and systems, when, with whose approval, and
keeps a record of what they did.** "Privileged" means access that can change
production, read sensitive data or turn off other controls: root on a server,
a database admin login, a cloud admin role.

{/* truncate */}

PAM is not to be confused with Linux PAM (Pluggable Authentication Modules),
the library that handles logins on a Linux host. Same acronym, different
thing.

## What PAM does

| Capability | Question it answers |
| --- | --- |
| **Access control** | Who may reach which system, as which account? |
| **Approval** | Did someone else agree before access was granted? |
| **Time limits** | When does the access end? |
| **Credential protection** | Does the person ever see the password or key? |
| **Session recording** | What exactly happened during the session? |
| **Audit** | Can we prove all of the above to an auditor later? |

## The vocabulary

**Standing privilege**: access that is always on. A key in
`authorized_keys`, an admin group membership. It's the default almost
everywhere, and it's what attackers use after stealing a credential.

**Just-in-time (JIT) access**: access granted for a task and removed
automatically afterwards. The goal is zero standing privilege: nobody holds
production access until they need it.
[More on JIT approvals](/blog/jit-access-approvals).

**PASM, privileged account and session management**: vaulting shared
credentials, brokering sessions so the user never sees them, and recording
those sessions.

**PEDM, privilege elevation and delegation management**: controlling what a
user may do once logged in, such as `sudo` policies and endpoint elevation.

**Break-glass access**: an emergency path for when the normal one is down,
used rarely and audited heavily.

**Least privilege**: the principle behind all of it. Each person gets the
minimum access needed, for the minimum time.

## Why teams adopt PAM

- **Stolen credentials are the common path in.** The
  [Verizon Data Breach Investigations Report](https://www.verizon.com/business/resources/reports/dbir/)
  has listed the use of stolen credentials among the top breach actions for
  years. Access that doesn't exist yet can't be stolen.
- **Auditors ask for it.** SOC 2 (CC6.1 to CC6.3), ISO 27001 (Annex A
  access control) and PCI DSS (requirements 7, 8 and 10) all ask who has
  access to what, how it was approved and how it is logged.
- **People leave.** Standing keys and group memberships survive offboarding
  far too often.

## PAM for SSH specifically

Most enterprise PAM suites started with Windows and vaulted passwords. For
Linux fleets, the privileged path is SSH, and it needs:

1. **A single entry point** (a gateway or bastion) instead of direct SSH to
   each host.
2. **Per-machine grants** tied to a person, not a shared key.
3. **JIT requests** for production, with approval in the console or chat.
4. **Session recording** taken at the gateway, out of reach of root on the
   target.
5. **MFA** at the gateway.
6. **Short-lived credentials** (SSH certificates) instead of static keys.

Orion Belt is a self-hosted SSH gateway built around those six. Targets run
an agent that [dials out](/blog/why-reverse-ssh-agents), so they need no
inbound SSH port.

## PAM vs. related tools

| Tool | Main job | Overlap with PAM |
| --- | --- | --- |
| Identity provider (IdP) | Who you are; SSO | Feeds PAM identities; doesn't grant server access |
| VPN | Network reachability | None at the account level |
| Bastion / jump host | One network entry point | The path, without approvals or recording ([comparison](/blog/bastion-vs-pam)) |
| Secrets manager | Storing credentials for apps | Overlaps with PASM vaulting |
| PAM | Who, when, approved by whom, recorded | — |

## FAQ

**Is PAM only for large enterprises?**
No. The need starts when more than one person has production access. What
changes with size is how much of it you automate.

**What's the difference between PAM and IAM?**
IAM manages identities and their general access. PAM is the subset that
governs the highest-risk access more strictly: approval, time limits and
recording.

**Does PAM replace sudo policies?**
No. PAM decides who gets onto the machine and as which account; sudo
policies (PEDM) decide what that account may do there.

## Related

- [Open-source PAM and SSH bastion options compared](/blog/open-source-pam-landscape)
- [SSH bastion host best practices](/blog/ssh-bastion-host-best-practices)
- [ReBAC for SSH machines](/blog/rebac-for-ssh-machines)
