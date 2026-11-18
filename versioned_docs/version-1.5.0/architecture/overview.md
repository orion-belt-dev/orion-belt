---
sidebar_position: 26
title: Architecture Overview
---

# Architecture overview

Orion Belt is a **self-hosted SSH access gateway** with PAM-style workflows.

## Components

1. **Gateway (server)** — SSH/SCP proxy (:2222), HTTP API + web console (:8080), ReBAC, MFA, recording, optional SSH CA  
2. **Agents** — on targets; dial **out** with reverse SSH (no inbound hole required on the host)  
3. **Clients** — web terminal, OpenSSH (`user+machine@gateway`), or `osh` / `ocp` / `oadmin`

```mermaid
flowchart TB
  subgraph Clients
    CLI["osh / ocp / oadmin"]
    OpenSSH["OpenSSH"]
    UI["Web /ui"]
  end

  GW["Gateway<br/>SSH :2222 · HTTP :8080"]
  DB[(PostgreSQL)]
  Rec["Recordings · audit"]
  Agent["Target agent"]

  CLI --> GW
  OpenSSH --> GW
  UI --> GW
  GW --> DB
  GW --> Rec
  Agent -->|"reverse SSH dial-out"| GW
```

## Trust boundary

- Operators and agents both connect **to** the gateway
- The gateway brokers sessions and can record / live-watch them
- Direct SSH to an agent host bypasses policy and recording

## Related

- [Sequence diagrams](./sequence-diagrams)
- [Component diagrams](./component-diagrams)
- [Try in 10 minutes](../quickstart/try-in-10-minutes)
