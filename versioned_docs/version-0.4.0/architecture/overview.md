---
sidebar_position: 26
title: Architecture Overview
---

# Architecture Overview

Orion-Belt consists of three main components:

1. **Server** – Central control plane managing agents, users, and permissions.
2. **Agents** – Installed on machines to enable controlled SSH access.
3. **Clients** – Users connect through the server to reach agents.

---

## Mermaid Diagram

```mermaid
graph TD
    Client -->|SSH| Server
    Server -->|Control| Agent
    Server -->|API| DB[(PostgreSQL)]
    Server -->|Logs| Storage[(Recordings)]
```
