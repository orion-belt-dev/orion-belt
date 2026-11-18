---
sidebar_position: 24
title: Server CLI Reference
---

# orion-belt-server CLI

### Commands

- **Start server**
```bash
orion-belt-server start --config /etc/orion-belt/server.yaml
```

* **Register agent**

```bash
orion-belt-server agent register \
  --name <agent> \
  --key "<ssh-public-key>" \
  --hostname <ip> \
  --tags environment=production,role=web
```

* **Create user**

```bash
orion-belt-server user create \
  --name alice \
  --email alice@example.com \
  --key "<ssh-public-key>" \
  --admin
```

* **Grant permission**

```bash
orion-belt-server permission grant \
  --user alice \
  --machine web-01 \
  --type ssh \
  --duration 3600
```

* **List agents**

```bash
orion-belt-server agent list
```

* **List users**

```bash
orion-belt-server user list
```

* **Debug and log**

```bash
orion-belt-server --log-level debug start
```
