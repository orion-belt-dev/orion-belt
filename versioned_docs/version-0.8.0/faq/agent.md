---
sidebar_position: 18
title: Agent FAQ
---

# Agent FAQ

**Q: How do I register an agent?**  
Use `orion-belt-server agent register --name <agent> --key <pubkey> --hostname <ip>`.

**Q: Do agents need a user account for each client?**  
Yes, the username in the client config must exist on the agent.

**Q: How do I restart an agent?**  
```bash
sudo systemctl restart orion-belt-agent
```

```
