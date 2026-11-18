---
sidebar_position: 19
title: Client FAQ
---

# Client FAQ

**Which key do I use locally?**  
The **private** key whose **public** half is registered on your Orion user.

**Do I need `osh`?**  
No — vanilla OpenSSH works: `ssh -p 2222 alice+web-01@gateway`. Web terminal works too.

**How do I request temporary access?**

```bash
osh web-01 --request-access --duration 1h --reason "Maintenance"
```

(Also available in the UI / ChatOps plugins when enabled.)

**Connection works but nothing is recorded?**  
You probably SSHed straight to the host. Always use the gateway.
