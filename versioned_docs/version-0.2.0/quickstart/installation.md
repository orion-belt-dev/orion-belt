---
sidebar_position: 1
title: Installation
---

# Installation

This guide walks you through installing Orion-Belt components. You can install via package managers, Docker, or from source.



## System Requirements

Component	CPU	RAM	Disk	OS
Server	2+ cores	4GB+	20GB+	Linux (Ubuntu 20.04+, RHEL 8+)
Agent	1+ core	512MB+	10GB+	Linux (any modern distro)
Client	Any	256MB	100MB	Linux, macOS, Windows

## Install from Source


```bash title="github "
# Clone the repository
git clone https://github.com/orion-belt-dev/orion-belt.git
cd orion-belt

# Build all components
make build

# Or build individually
make build-server
make build-client
make build-agent
```
:::success Verify Installation
After installation, verify each component:orion-server --version,orion-agent --version,osh --version
:::