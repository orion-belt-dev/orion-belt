---
sidebar_position: 17
title: Server FAQ
---

# Server FAQ

**Q: What ports are required for Orion-Belt?**  
- Default SSH port: 2222  
- API port: 8080 (configurable)

**Q: Can I run multiple servers?**  
Yes, but each server manages its own set of agents. Multi-server orchestration is not built-in.

**Q: Where are logs stored?**  
- `/var/log/orion-belt/server.log`

**Q: Can I use TLS for server?**  
Yes, configure cert/key in `server.yaml`.

