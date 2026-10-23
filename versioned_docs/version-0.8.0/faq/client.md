---
sidebar_position: 19
title: Client FAQ
---

# Client FAQ

**Q: Which key should I use in client.yaml?**  
Use the **private key** that corresponds to the public key registered on the server.

**Q: How do I list machines?**  
```bash
./osh --list
```

**Q: How do I request temporary access?**

```bash
./osh agent --request-access --reason "Maintenance" --duration 3600
```