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

**License / Commons Clause?**  
Free to self-host and use internally; cannot sell Orion Belt as a product/service.
