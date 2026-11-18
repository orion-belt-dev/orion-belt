---
slug: ssh-bastion-host-best-practices
title: SSH bastion host best practices — a hardening checklist
description: A checklist for running an SSH bastion or jump host safely in 2026 — identity, keys, MFA, network exposure, least privilege, session recording, logging and break-glass — with OpenSSH settings and where a PAM gateway replaces manual work.
authors: [orion]
tags: [ssh, security, ops]
keywords: [SSH bastion best practices, jump host hardening, bastion host security, SSH hardening checklist, secure SSH access, ProxyJump]
---

**A bastion host is the single door into your servers, so it has to be the
most locked-down machine you run.** This checklist covers the controls that
matter, in the order they usually go wrong, with the OpenSSH settings for a
plain jump host and what a PAM gateway does for you.

{/* truncate */}

## 1. One identity per person

Shared accounts (`ubuntu`, `admin`, `deploy`) destroy attribution. Every
person gets their own login on the bastion, even if they land on a shared
account on the target.

- Plain bastion: one Unix user per person, provisioned from your directory.
- Gateway: users exist at the gateway; the target account (`remote_user`) is
  part of the grant. Orion Belt logs both who you are and which account you
  used.

## 2. Keys only, no passwords

```text
# /etc/ssh/sshd_config
PasswordAuthentication no
KbdInteractiveAuthentication no
PermitRootLogin no
PubkeyAuthentication yes
```

Prefer ed25519 keys. Hardware-backed keys (`ed25519-sk`, `ecdsa-sk`) keep
the private key on a FIDO2 token, so a stolen laptop disk is not a stolen key.

## 3. Short-lived credentials beat key rotation

Long-lived keys in `authorized_keys` outlive the people who own them.
[SSH certificates](https://man.openbsd.org/ssh-keygen#CERTIFICATES) signed
by a CA expire on their own. With OpenSSH, set `TrustedUserCAKeys` on the
hosts and issue certificates with `ssh-keygen -s ca -V +8h`. Orion Belt can
run that CA for you ([optional SSH CA](/blog/optional-ssh-ca)).

## 4. MFA at the door

The bastion is where MFA belongs: one place to enforce it, rather than every
host. Options: FIDO2 SSH keys (the touch is the second factor), TOTP through
PAM, or a gateway that requires TOTP or WebAuthn for the console
([MFA and WebAuthn](/blog/mfa-webauthn-ssh)).

## 5. Expose as little as possible

- Only the bastion accepts SSH from outside. Targets accept SSH only from the
  bastion's address, or not at all.
- With a reverse-agent gateway, targets open **no inbound port**: the agent
  dials out ([why](/blog/why-reverse-ssh-agents)). There is nothing to scan.
- Disable features you don't use on the bastion itself:

```text
AllowTcpForwarding no      # unless you rely on ProxyJump through it
X11Forwarding no
AllowAgentForwarding no
PermitTunnel no
```

Agent forwarding on a shared bastion lets anyone with root on it use your
keys while you're connected. Use `ProxyJump` (`ssh -J bastion target`)
instead: the connection to the target is end to end from your laptop.

## 6. Least privilege, and time-boxed

Standing access to production is the norm and the problem. Grant access per
machine, and for privileged access, for a limited time after approval.
In [NIST SP 800-53 Rev. 5](https://csrc.nist.gov/pubs/sp/800/53/r5/upd1/final),
the controls auditors map this to are AC-6 (least privilege) and AC-2
(account management).

In Orion Belt, grants are per user, machine and remote user, and
[just-in-time requests](/blog/jit-access-approvals) expire on their own.

## 7. Record sessions off the target

Logs on the target can be edited by root on the target. Record at the
bastion. On a plain jump host with `ProxyJump` you can't see inside the
session (it's encrypted end to end), so recording needs a gateway that
terminates SSH. See
[how to audit SSH access on Linux](/blog/how-to-audit-ssh-access-linux).

## 8. Ship logs, alert on the unusual

Send bastion logs off the box as they're written: sshd at `LogLevel VERBOSE`
(logs key fingerprints), the gateway's audit log, and metrics. Alert on
logins outside working hours, new source addresses and failed MFA bursts.

## 9. Patch it first

The bastion is internet-facing and holds the keys to everything. It goes
first in every OpenSSH and kernel update cycle, not last.

## 10. Plan break-glass before you need it

When the bastion or your identity provider is down, how do you get in? Write
it down: a sealed local admin credential, who can use it, and how its use is
audited and rotated afterwards.

## Checklist

- [ ] One identity per person; no shared bastion logins
- [ ] Keys only; root login off
- [ ] Short-lived certificates or a gateway instead of static keys
- [ ] MFA enforced at the bastion
- [ ] Targets accept SSH only via the bastion (or open no port at all)
- [ ] Agent forwarding off; `ProxyJump` for hops
- [ ] Per-machine, time-boxed grants for privileged access
- [ ] Sessions recorded off the target
- [ ] Logs shipped off-host with alerts
- [ ] Bastion patched first
- [ ] Documented break-glass

## FAQ

**Is a bastion host still needed with a VPN?**
A VPN gives network access; it doesn't decide which machine a person may log
into, record the session or expire access. Many teams run both.

**ProxyJump or agent forwarding?**
ProxyJump. Agent forwarding exposes your agent to anyone with root on the
bastion for as long as you're connected.

**What's the difference between a bastion and PAM?**
A bastion controls the network path. PAM adds who may access what, when, with
whose approval, and a record of it. More in
[SSH bastion vs PAM](/blog/bastion-vs-pam).
