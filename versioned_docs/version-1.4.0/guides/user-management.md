---
sidebar_position: 5
title: User Management
---

# User management

Users authenticate to Orion Belt with SSH keys (and optional password+TOTP / WebAuthn). Prefer the **Users** page in the web console for day-to-day work.

```mermaid
flowchart LR
  Admin["Admin"] -->|create / grant| User["User"]
  User -->|SSH / UI| GW["Gateway"]
  GW -->|allowed remote users| Agent["Agent"]
```

## Create a user (UI)

**Users** → add user → paste SSH **public** key → assign role (admin / operator / auditor / user).

## Create a user (CLI)

```bash
orion-belt-server user create \
  --name alice \
  --email alice@example.com \
  --key "$(cat ~/.ssh/id_ed25519.pub)"
```

First admin is usually created by `setup` / docker-quickstart, not this command.

## Grants

Access is **per machine** plus allowed **remote users** (Linux accounts on the agent):

```bash
orion-belt-server permission grant \
  --user alice --machine web-01 --type both --remote-users root
```

Also editable under **Permissions** in the UI.

## Temporary / JIT access

Users can request time-boxed access; admins approve in the UI (or ChatOps). See client FAQ / requests UI.

## List / delete

- UI **Users**
- CLI: `orion-belt-server user list` / delete equivalents when using the server CLI on the gateway

## Tips

- Prefer ed25519 (or `sk-` FIDO) keys  
- Don’t share private keys  
- Direct-to-host SSH bypasses Orion — train operators to use the gateway only
