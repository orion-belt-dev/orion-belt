---
slug: mfa-webauthn-ssh
title: MFA and WebAuthn for a self-hosted SSH PAM console
description: How Orion Belt splits console MFA (TOTP, WebAuthn) from SSH public keys and FIDO sk- keys — configuration pitfalls, rp_id origins, and practical hardening.
authors: [orion]
tags: [security, ssh, pam]
keywords: [WebAuthn SSH, MFA privileged access, YubiKey PAM, self-hosted MFA]
---

Operators will reject MFA that fights SSH muscle memory. Orion Belt splits the problem: **protect the web console** with TOTP and WebAuthn, and keep **SSH on keys** — including FIDO `sk-*` keys — that OpenSSH already understands.

{/* truncate */}

If you are hardening a **self-hosted privileged access** gateway, this is the MFA model to expect and the footguns to avoid.

## Two planes of authentication

| Plane | Mechanisms | Goal |
| --- | --- | --- |
| Web console `/ui` | SSH pubkey login, password+TOTP, WebAuthn | Protect admin/approver UX |
| SSH to gateway | Registered user keys, optional FIDO SSH keys | Day-to-day access |

Confusing these planes creates tickets like “my YubiKey works in the browser but not in SSH” — different protocols, different registration paths.

## Console MFA

### TOTP

Familiar authenticator-app second factor. Useful when hardware keys are unavailable.

### WebAuthn / FIDO2

Browser security keys bound to:

- **`rp_id`** — hostname only (e.g. `orion.example.com` or `localhost`)  
- **`origins`** — full origins the UI is served from (`https://orion.example.com`, maybe a Vite dev origin)

If `rp_id` / origins disagree with the URL in the address bar, registration and login fail in opaque ways. Fix config before blaming the YubiKey.

Docker env knobs include `ORION_WEBAUTHN_ENABLED`, `ORION_PUBLIC_HOST`, and `ORION_PUBLIC_ORIGIN` — see `.env.server.example` in the repo.

## SSH keys and FIDO

Users register SSH **public** keys on their Orion identity. OpenSSH then connects as:

```bash
ssh -i ~/.ssh/id_ed25519 -p 2222 alice+web-01@gateway
```

For hardware-backed SSH keys (`sk-ssh-ed25519@openssh.com`, etc.), register the public key the same way and touch the device when OpenSSH prompts. Details: main repo `docs/openssh-clients.md`.

## Recommended hardening ladder

1. Lab: pubkey admin via quickstart  
2. Staging: enable TOTP for admins  
3. Production: WebAuthn for admins/approvers + network allowlist on UI  
4. Require MFA for sensitive console actions as your threat model demands  

Also encrypt session recordings — MFA does not help if casts sit plaintext on disk ([session recording](/blog/session-recording-is-hard)).

## FAQ

**Can I force MFA on every SSH connection?**  
SSH MFA UX varies; many teams prefer strong keys + gateway policy + recorded sessions. Console MFA is the high-ROI control for approvals and replay.

**Does WebAuthn replace SSH keys?**  
No. It protects browser sessions to the control plane.

## Related

- [Security guide](/docs/guides/security)  
- [Optional SSH CA](/blog/optional-ssh-ca)  
- [Try in 10 minutes](/docs/quickstart/try-in-10-minutes)
