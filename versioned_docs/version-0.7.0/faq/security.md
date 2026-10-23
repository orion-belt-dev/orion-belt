---
sidebar_position: 20
title: Security FAQ
---

# Security FAQ

**Q: Can I share keys between users?**  
No, each user must have a unique SSH key.

**Q: Can I revoke access instantly?**  
Yes, use `orion-belt-server permission revoke --user <user> --machine <machine>`.

**Q: How long are session recordings kept?**  
Defined in server.yaml under `session.recording.retention_days`.

**Q: How to rotate keys?**  
1. Register new key for user/agent.  
2. Update config on clients or agents.  
3. Remove old key from server.
