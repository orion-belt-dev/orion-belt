---
sidebar_position: 2
title: Server Setup
---

# Orion-Belt Server Setup

The **Orion-Belt Server** is the central control plane that manages agents, users, permissions, and session recording.

---

## Architecture Overview

```mermaid
flowchart LR
    Client["User Client (osh)"] -->|SSH via server| Server["Orion-Belt Server"]
    Server -->|SSH Proxy| Agent["Agent Machine"]
    Server --> DB["Database (PostgreSQL)"]
    Server --> Logs["Session & Audit Logs"]
    Server --> Plugins["Optional Plugins (Slack, Audit, etc.)"]
```
## Installation
### Dependencies
* Linux server (Ubuntu/Debian recommended)

* PostgreSQL

* OpenSSH server

* curl, tar
### Steps 
```bash
sudo apt update
sudo apt install -y curl openssh-server postgresql tar
sudo systemctl enable postgresql
sudo systemctl start postgresql
sudo -u postgres psql -c "ALTER USER postgres PASSWORD 'postgres';"
sudo mkdir -p /opt/orion-belt/bin /etc/orion-belt/plugins /var/lib/orion-belt/recordings /var/log/orion-belt
sudo chmod -R 755 /opt/orion-belt /etc/orion-belt /var/lib/orion-belt /var/log/orion-belt
sudo ssh-keygen -t ed25519 -f /etc/orion-belt/ssh_host_key -N ""

```

### Configuration

**File**: `/etc/orion-belt/server.yaml`

```yaml
server:
  host: "0.0.0.0"
  port: 2222
  api_port: 8080
  ssh_host_key: "/etc/orion-belt/ssh_host_key"
  api_endpoint: "http://localhost:8080/api"
  plugin_dir: "/etc/orion-belt/plugins"

database:
  driver: "postgres"
  connection_string: "postgres://postgres:postgres@localhost:5432/postgres?sslmode=disable"

recording:
  enabled: true
  storage_path: "/var/lib/orion-belt/recordings"
  retention_days: 90

auth:
  rebac_enabled: true
  allow_temp_access: true

plugins:
  audit-logger:
    log_path: "/var/log/orion-belt/audit.log"
    enabled: true
  slack-notifications:
    webhook_url: "https://hooks.slack.com/services/YOUR/WEBHOOK"
    channel: "#security"
    enabled: true

```

---

## Start the Server

```bash
sudo /opt/orion-belt/bin/orion-belt-server --config /etc/orion-belt/server.yaml
```

- `--config <file>`: Specify a config file
- `--log-level <level>`: debug, info, warn, error

### Verify ports:

```bash
ss -tulnp | grep 2222  # SSH Proxy
ss -tulnp | grep 8080  # REST API
```

### cli Usage

```bash
# Show active config
orion-belt-server config path

# Register agent
orion-belt-server agent register \
  --name agent01 \
  --key "$(cat /etc/orion-belt/agent_key.pub)" \
  --hostname 192.168.0.110 \
  --port 22 \
  --tags environment=production,role=web

# Create a user
orion-belt-server user create \
  --name alice \
  --email alice@example.com \
  --key "$(cat ~/.ssh/id_rsa.pub)" \
  --admin
```
## Quickstart Flow 

```
flowchart TD
    subgraph Server
        S[Orion-Belt Server] --> DB[Database]
        S --> Logs[Logs & Recordings]
    end
    Client -->|SSH| S
    Agent -->|Heartbeat & Session| S
    S -->|Proxy| Agent

```

## FAQ

- **Do I need unique Linux users per client on agents?**
  - Yes, each client maps to a Linux user for session isolation.

- **Which keys are used?**

  - Server: Private (`ssh_host_key`)
  - Agent: Public (`agent_key.pub`)
  - Client: Private (`client_key`)

- **How long are session recordings kept?**
  - Controlled by `recording.retention_days`.