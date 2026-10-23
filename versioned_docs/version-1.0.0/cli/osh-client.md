---
sidebar_position: 25
title: Client CLI Reference
---

# osh – Orion-Belt SSH Client

### Commands

- **List machines**
```bash
./osh --list
```

* **Connect to a machine**

```bash
./osh user@machine -c /etc/orion-belt/client.yaml
```

* **Request temporary access**

```bash
./osh user@machine --request-access --reason "Maintenance" --duration 3600
```

* **Specify config or username**

```bash
./osh agent -c /etc/orion-belt/client.yaml -u alice
```

* **Notes**

  * The `client.yaml` must include the private key corresponding to the public key registered on the server.
  * Each user must exist on the agent machine for SSH to succeed.
