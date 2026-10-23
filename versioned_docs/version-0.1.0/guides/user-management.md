---
sidebar_position: 5
title: User Management
---

# Orion-Belt User Management

The Orion-Belt server manages all users that can connect to agents via the `osh` client. This page covers **creating, listing, deleting users**, and granting them access.

---

## User Lifecycle

```mermaid
flowchart LR
    Admin["Server Admin"] -->|Create| User["User (client)"]
    User -->|Request access| Server["Orion-Belt Server"]
    Server -->|Grant/Revoke| Permissions["Permissions on Machines"]
```

---

## Create a User

```bash
orion-belt-server user create \
  --name alice \
  --email alice@example.com \
  --key "$(cat ~/.ssh/id_rsa.pub)" \
  [--admin]
```

**Notes:**

* `--key` is the user's SSH public key.
* Use `--admin` to grant administrative rights.

**Example:**

```bash
orion-belt-server user create \
  --name admin \
  --email admin@example.com \
  --key "$(cat ~/.ssh/id_ed25519.pub)" \
  --admin
```

**Output:**

```
User created successfully:
  Username: admin
  Email: admin@example.com
  User ID: uuid-xyz-789
  Admin: true
```

---

## List Users

```bash
orion-belt-server user list
```

**Output:**

```
USERNAME  EMAIL                  ADMIN  CREATED
--------  -----                  -----  -------
admin     admin@example.com      yes    2026-01-04 08:00:00
alice     alice@example.com      no     2026-01-04 09:15:30
bob       bob@example.com        no     2026-01-04 10:30:15
```

---

## Delete a User

```bash
orion-belt-server user delete <username>
```

**Example:**

```bash
orion-belt-server user delete alice
# Output: User 'alice' deleted successfully.
```

---

## FAQ

* **Do I need to create a local Linux user on the agent for each Orion-Belt user?**
  Yes, the Orion-Belt client maps to a Linux user on the agent machine for proper isolation.

* **What key should I provide when creating a user?**
  Only the **public SSH key** of the user.

* **Can I make a user an admin later?**
  Yes, you can update user roles via the CLI (future extension).

* **How to check if a user can connect?**
  Use `osh --list` as the user or check `orion-belt-server permission list <username>`.
