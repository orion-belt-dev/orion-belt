---
sidebar_position: 27
title: Sequence Diagrams
---

# Sequence Diagrams

### User connecting to agent

```mermaid
sequenceDiagram
    User->>Server: Request SSH connection
    Server->>PermissionDB: Verify access
    Server->>Agent: Proxy SSH session
    Agent-->>Server: Session ready
    Server-->>User: Interactive shell
```
