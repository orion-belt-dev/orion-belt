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

* **Explain access** (1.4.0)

```bash
./osh explain root@web-01               # why you can or cannot reach it
./osh explain root@web-01 --for alice   # admins and auditors, for someone else
```

* **RDP and VNC** (1.4.0) — see [RDP and VNC](../guides/rdp-vnc)

```bash
./osh rdp win-01                                  # local port relayed to RDP on win-01
./osh vnc lab-02 --listen 127.0.0.1:15900
./osh rdp win-01 -r --reason "patching" -d 1800   # request rdp access first
```

* **Kubernetes** (1.4.0) — see [Kubernetes](../guides/kubernetes)

```bash
./osh kube prod-eu -r --scope payments/edit --reason "INC-42" -d 1800
export KUBECONFIG=~/.orion-belt/kube/prod-eu.yaml
kubectl get pods
```

* **Specify config or username**

```bash
./osh agent -c /etc/orion-belt/client.yaml -u alice
```

* **Notes**

  * The `client.yaml` must include the private key corresponding to the public key registered on the server.
  * Each user must exist on the agent machine for SSH to succeed.
