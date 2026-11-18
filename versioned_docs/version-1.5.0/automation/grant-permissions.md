---
sidebar_position: 30
title: Grant Permissions Automation
---

# Automation: Grant Permissions

Grant multiple permissions programmatically:

```bash
#!/bin/bash
declare -A perms=(
  ["alice"]="web-01 db-01"
  ["bob"]="db-01"
)

for user in "${!perms[@]}"; do
  for machine in ${perms[$user]}; do
    orion-belt-server permission grant \
      --user "$user" \
      --machine "$machine" \
      --type both \
      --duration 3600
  done
done
```

* Automates **user onboarding and access control**.

