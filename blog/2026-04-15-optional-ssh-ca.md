---
slug: optional-ssh-ca
title: Optional SSH certificate authority for self-hosted bastions
description: When to enable an SSH CA in a PAM gateway, how short-lived certificates reduce key sprawl, and why Orion Belt keeps SSH CA optional for labs.
authors: [orion]
tags: [ssh, security, architecture]
keywords: [SSH CA, SSH certificates, self-hosted bastion, privileged access certificates]
---

Long-lived `authorized_keys` entries are how SSH access rots. An **SSH certificate authority (CA)** issues short-lived user and host certificates instead. Powerful — and easy to force on users before they have a working bastion.

{/* truncate */}

Orion Belt treats SSH CA as **optional**. You can run reverse agents, recording, JIT, and MFA without it. This article explains when to turn CA on for a **self-hosted SSH PAM** deployment.

## What an SSH CA changes

| Without CA | With CA |
| --- | --- |
| Standing user keys on many systems | Short-lived user certs from the gateway |
| TOFU / known_hosts drift | Host certificates for gateway/agents |
| Revocation by hunting files | Wait for expiry (+ revoke CA-side as designed) |

OpenSSH has supported certificates for years; PAM/bastion products wrap the operational bits humans forget.

## Why optional?

Because the **10-minute path** should not require understanding certificate principals, `AuthorizedPrincipalsFile`, and CA key ceremonies. Labs need wow-first. CA is a production maturity step — see [Designing a 10-minute install path](/blog/ten-minute-install-path).

## When you should enable it

Enable SSH CA when:

- Key inventory is already painful  
- Contractors come and go weekly  
- You want host identity pinned via Host CA  
- Someone on the team can own CA key backup / rotation  

Keep it off when:

- You are still validating reverse agents and recording  
- Nobody owns secret management for the CA master key  

Orion encrypts CA material at rest when enabled (`ssh_ca.master_key` style config in the main repo). Do not use example secrets.

## Operational checklist

1. Backup the CA master key like a root of trust  
2. Set host principals to the DNS/IPs clients actually use  
3. Document how agents receive host certs on enrollment  
4. Monitor expiry and clock skew (certs are time-sensitive)  
5. Pair with recording — certificates are not an audit trail  

Full reference: [SSH_CA.md](https://github.com/orion-belt-dev/orion-belt/blob/master/docs/SSH_CA.md) in the product repo.

## Related

- [MFA and WebAuthn](/blog/mfa-webauthn-ssh)  
- [ReBAC for SSH machines](/blog/rebac-for-ssh-machines)  
- [Security best practices](/docs/guides/security)
