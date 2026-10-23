---
sidebar_position: 10
title: Security Best Practices
---

# Security best practices

Orion Belt concentrates trust on the **gateway**. Harden that first.

## Gateway

- Put **2222** / HTTPS behind a firewall or allowlist; don’t expose Postgres
- Use a strong `auth.jwt_secret` (never ship example values)
- Prefer **HTTPS** for `/ui` and the API in production
- Enable **recording encryption** (`recording.encryption_key`)
- Turn on **MFA** (TOTP and/or WebAuthn) for console access
- Review [DEPLOYMENT_HARDENING.md](https://github.com/orion-belt-dev/orion-belt/blob/master/docs/DEPLOYMENT_HARDENING.md) in the main repo

## Agents

- Agents only need **outbound** reachability to the gateway (usually :2222)
- Keep agent private keys `0600`; never commit them
- Prefer the **Add agent** install script so keys aren’t copied by hand
- Patch agent hosts like any production Linux box

## Access control

- Least privilege: grant per-machine access + specific **remote users**
- Prefer **JIT / temporary access** for break-glass and contractors
- Prefer short-lived **SSH CA** certs when enabled over standing keys

## Sessions & audit

- Require all human SSH through the gateway (no direct-to-host habit)
- Restrict who can **playback** / **live watch**
- Set retention deliberately; recordings often contain secrets typed into shells

## Keys

- Unique keys per user and per agent
- Rotate by adding the new key before removing the old
- FIDO/`sk-*` SSH keys are supported for operators

## Identity roadmap

OIDC / external IdP is on the post-1.0 roadmap. Today: SSH keys, optional password+TOTP, WebAuthn, API keys/JWT.

## FAQ

**LDAP/SSO today?**  
Not yet — use local users + keys/MFA; watch the roadmap for OIDC.

**Commons Clause?**  
Free to self-host and use internally; you cannot sell Orion Belt as a product/service. See the LICENSE on GitHub.
