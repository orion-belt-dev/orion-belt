---
sidebar_position: 20
title: Security FAQ
---

# Security FAQ

**Can users share keys?**  
No — unique SSH keys per user (and per agent).

**Can I revoke access instantly?**  
Yes — remove the grant in **Permissions** / UI, or revoke via CLI (`permission revoke` / equivalent admin API).

**How long are recordings kept?**  
`recording.retention_days` in server config (not `session.recording.*` from older docs).

**How do I rotate keys?**  
1. Register the new public key  
2. Update clients/agents to use the new private key  
3. Remove the old key from Orion  

**MFA?**  
TOTP + WebAuthn for the console; FIDO `sk-*` SSH keys for OpenSSH. See server setup / main repo SETUP.md.

**License?**  
GNU AGPL v3.0 only, since 1.5.0 (earlier releases were Apache 2.0 + Commons Clause). Free to use, self-host and run commercially. If you distribute it, or offer a modified version over a network, you publish your source under the same license. See the [LICENSE](https://github.com/orion-belt-dev/orion-belt/blob/master/LICENSE).
