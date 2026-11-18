---
sidebar_position: 29
title: Register Multiple Agents
---

# Automation: Register Multiple Agents

You can batch register agents using a shell script.

```bash
#!/bin/bash
declare -A agents=(
  ["web-01"]="10.0.1.10"
  ["db-01"]="10.0.2.10"
  ["cache-01"]="10.0.3.10"
)

for name in "${!agents[@]}"; do
  ip="${agents[$name]}"
  orion-belt-server agent register \
    --name "$name" \
    --key "$(cat /tmp/${name}.pub)" \
    --hostname "$ip" \
    --tags environment=production
done
```

* Useful for **multi-server deployments**.

