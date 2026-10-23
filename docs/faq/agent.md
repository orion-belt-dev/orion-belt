---
sidebar_position: 18
title: Agent FAQ
---

# Agent FAQ

**How do I register an agent?**  
Prefer console **Add agent** (install script). Or register the agent public key via API / `orion-belt-server agent register`, then start `orion-belt-agent`.

**Does the gateway need inbound access to the agent?**  
No. The agent dials **out** to the gateway (usually port **2222**).

**Do I need a Linux user for every Orion user?**  
No. Grant access with specific **remote users** (e.g. `root`, `ubuntu`) that exist on the host.

**How do I restart an agent?**

```bash
sudo systemctl restart orion-belt-agent
```

**Docker lab agent offline?**  
Check `ORION_SERVER_HOST` (`host.docker.internal` or LAN IP), matching `ORION_AGENT_NAME`, and `./agent-key` permissions.

**Where is the private key?**  
On the agent only (`/etc/orion-belt/agent_key` or `./agent-key`). The gateway stores the public key / registration.
