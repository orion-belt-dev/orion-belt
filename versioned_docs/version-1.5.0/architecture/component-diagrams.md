---
sidebar_position: 28
title: Component Diagrams
---

# Component Diagrams

```mermaid
graph LR
    Server[Orion-Belt Server]
    Agent[Agent Machine]
    Client[User Client]
    DB[(PostgreSQL)]
    Storage[(Session Storage)]

    Client --> Server
    Server --> Agent
    Server --> DB
    Server --> Storage
```
