---
slug: ssh-key-management-at-scale
title: SSH key management at scale — authorized_keys sprawl, certificates and gateways compared
description: Three ways to manage SSH access across a fleet — distributing authorized_keys, SSH certificates from a CA, and an access gateway — with how each handles onboarding, offboarding, rotation and audit, and when to combine them.
authors: [orion]
tags: [ssh, security, ops]
keywords: [SSH key management, authorized_keys management, SSH certificates, SSH CA, SSH key rotation, offboarding SSH keys, SSH key sprawl]
---

**SSH key sprawl is what happens when every person's public key is copied
into `authorized_keys` on every server they ever needed.** Nobody knows
which keys are still in use, offboarding means editing hundreds of files,
and a key from a laptop retired three years ago still opens production.
There are three ways out. This post compares them.

{/* truncate */}

## The three models

| | `authorized_keys` distribution | SSH certificates (CA) | Access gateway |
| --- | --- | --- | --- |
| Where trust lives | Each host's key files | The CA public key on each host | The gateway |
| Onboarding | Push key to N hosts | Issue a certificate | Create user + grant |
| Offboarding | Remove key from N hosts | Stop issuing; old certs expire | Disable user; access ends now |
| Credential lifetime | Forever | Hours, by design | Session; grants can expire |
| Per-machine access | Which hosts got the key | Principals in the cert | Grants per machine |
| Approval workflow | None | None built in | Built in |
| Session recording | No | No | Yes |
| Host changes needed | Every change | Once (`TrustedUserCAKeys`) | Agent install, once |

## Model 1: distributing authorized_keys

Config management (Ansible, Puppet, Chef) pushes keys to hosts. It works,
and most teams start here.

It breaks down on three things:

- **Offboarding is a deploy.** Until the run reaches every host, the key
  still works. Hosts that miss the run keep it.
- **Keys never expire.** OpenSSH supports an `expiry-time` option in
  `authorized_keys`, but almost nobody uses it.
- **No record of use.** You know who *could* log in, not who did.

If you stay here, at least set `LogLevel VERBOSE` in sshd so logins record
the key fingerprint, and audit keys against your directory regularly.

## Model 2: SSH certificates

OpenSSH has supported certificates since 5.4. A CA signs a user's public key
with a validity window and a list of principals (the accounts it may log in
as). Hosts trust the CA, not individual keys
([ssh-keygen(1) CERTIFICATES](https://man.openbsd.org/ssh-keygen#CERTIFICATES)).

```text
# On each host, once:
TrustedUserCAKeys /etc/ssh/user_ca.pub

# Issuing a cert valid for 8 hours, as user "deploy":
ssh-keygen -s user_ca -I alice@example.com -n deploy -V +8h id_ed25519.pub
```

Certificates fix expiry and offboarding: stop issuing and access ends when
the last certificate runs out. Host certificates fix the other direction,
replacing "trust on first use" with a CA-signed host key.

What they don't give you: an approval step, a record of the session, or
per-request scoping, unless you build the issuing service yourself. The CA
key also becomes the most sensitive secret you own.

## Model 3: an access gateway

Users authenticate to a gateway; the gateway decides per request whether they
may reach a machine as a given account, then brokers the session. Users'
keys never need to be on the targets.

With Orion Belt:

- Users register their own keys with the gateway (`osh keys add`), and
  remove them the same way.
- Grants are per user, machine and remote user. Privileged ones are
  requested [just in time](/blog/jit-access-approvals) and expire.
- Every session is recorded at the gateway.
- Targets run an agent that [dials out](/blog/why-reverse-ssh-agents); you
  don't manage keys on them at all.

## Combining them

The models stack well. Orion Belt includes an
[optional SSH CA](/blog/optional-ssh-ca): it issues short-lived user
certificates for operators and host certificates for the gateway and agents.
That gives you short-lived credentials (model 2) behind a gateway with grants,
approvals and recording (model 3). Static `authorized_keys` remain only for
break-glass.

## A migration path

1. Turn on `LogLevel VERBOSE` and find out which keys are actually used.
2. Put a gateway in front of production and move people to it.
3. Turn on short-lived certificates.
4. Remove personal keys from `authorized_keys` on targets, host group by host
   group, keeping one documented break-glass key.

## FAQ

**How do I revoke an SSH key on all servers at once?**
With `authorized_keys`, you can't, only push a change and wait. With
certificates, use a revocation list (`RevokedKeys`) or wait for expiry. With a
gateway, disable the user or remove the key at the gateway, and it takes
effect on the next connection.

**How often should SSH keys be rotated?**
Rotation matters less when credentials are short-lived. With certificates
valid for hours, the user key itself can stay put; protect it with a
hardware token instead.

**Are SSH certificates better than keys?**
Certificates are keys plus a signature with an expiry and principals. They
fix expiry and offboarding; they don't add approvals or recording.

## Related

- [SSH bastion host best practices](/blog/ssh-bastion-host-best-practices)
- [MFA and WebAuthn for SSH](/blog/mfa-webauthn-ssh)
