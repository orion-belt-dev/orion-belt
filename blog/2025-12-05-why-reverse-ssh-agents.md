---
slug: why-reverse-ssh-agents
title: Why reverse SSH agents beat opening inbound ports on every server
description: Learn how reverse SSH tunnels work for bastion and PAM gateways, the security tradeoffs versus inbound SSH, and why Orion Belt agents dial out instead of exposing port 22.
authors: [orion]
tags: [ssh, architecture, security, self-hosted]
keywords: [reverse SSH, SSH bastion, reverse tunnel, self-hosted PAM, no inbound SSH]
---

If you run Linux servers at any scale, you already know the uncomfortable default: **someone opens SSH from somewhere**. A bastion host, a VPN, a “temporary” security-group rule that outlives the outage. Traditional **SSH bastion** designs still often assume the gateway (or the operator) can reach port 22 on the target.

{/* truncate */}

Orion Belt takes a different default for its **self-hosted PAM / SSH access gateway**: agents on target machines **dial out** to the gateway over reverse SSH. Operators never need inbound SSH on the application hosts. This article explains why that model exists, how it compares to classic bastions, and which tradeoffs you should accept with eyes open.

## The problem with inbound SSH (even “just from the bastion”)

A classic pattern looks like this:

```text
Operator → Bastion → (SSH :22) → Target
```

That path creates recurring pain:

1. **Firewall inventory grows forever** — every VPC, VLAN, and cloud account needs rules so the bastion can reach targets.
2. **Targets stay in the attack surface** — anything that accepts inbound SSH can be scanned, credential-stuffed, or hit by a compromised jump box.
3. **NAT and ephemeral environments fight you** — laptops, CI runners, and locked-down cloud subnets often cannot be “reached”; they can only dial out.
4. **Audit is incomplete** — if anyone still SSHs direct to the host, your bastion’s session recording never sees it.

A **Teleport alternative** or commercial PAM product will solve pieces of this with agents, workers, or IoT tunnels. The underlying idea is the same: **move reachability so the target initiates connectivity**.

## What “agents dial out” means

In Orion Belt:

```text
Operator → Gateway (SSH :2222 / HTTPS UI)
                ↑
         reverse SSH
                ↑
              Agent on target
```

1. You install a small **agent** on the machine you want to manage.
2. The agent authenticates to the **gateway** and keeps a reverse tunnel open (outbound from the target’s perspective).
3. When an operator connects to the gateway (CLI, OpenSSH `user+machine@gateway`, or web terminal), the gateway **brokers** the session through that tunnel.
4. Policy (ReBAC), MFA on the console, JIT approvals, and **session recording / live watch** all sit on the gateway path.

The target still runs Linux users and SSH semantics for the remote account you land on (`root`, `ubuntu`, …). What changes is **who is allowed to open a connection toward that host from the network**.

## Security tradeoffs (honest)

### Gains

- **No inbound SSH required on targets** for the Orion path — useful for hardened subnets and “deny all ingress” baselines.
- **One control plane** for authentication, authorization, and recording.
- **Works behind NAT** as long as the agent can reach the gateway on the SSH port (typically **2222**).

### Costs

- **Offline agent = unreachable host** until it reconnects. There is no magic wake-up if the process is dead.
- **The gateway becomes high-value infrastructure.** Treat it like a domain controller for SSH: patch it, restrict who can reach `:2222` / the UI, encrypt recordings, require MFA for admins.
- **You must trust and update the agent binary** on every enrolled machine — same class of risk as any other node agent (Teleport, monitoring, backup).

If your environment cannot allow **outbound** connections from targets to a central gateway, reverse agents are the wrong tool. Pick a model that matches your network policy.

## Reverse tunnels vs VPN vs classic bastion

| Approach | Typical exposure | Recording / JIT | Ops weight |
| --- | --- | --- | --- |
| Direct SSH to hosts | Inbound :22 everywhere | DIY | Low features |
| Classic bastion | Inbound to bastion + often bastion→host :22 | Rare / custom | Low–medium |
| Full-mesh VPN | Broad L3 reachability | Separate tools | Medium |
| Reverse-agent gateway (Orion Belt) | Inbound mainly to gateway; agents outbound | Built-in path | Medium, SSH-focused |

VPNs solve “how do packets route.” They do not automatically give you **privileged access management**: approvals, per-machine grants, or session playback. A **self-hosted privileged access** gateway sits *above* connectivity.

## When reverse agents are the right default

Choose this model if most of these are true:

- Your privileged access problem is **mostly SSH**
- You want a **self-hosted SSH bastion** without SaaS lock-in
- Security wants **no inbound SSH** on app tiers
- You need **recording + JIT** without buying a CyberArk-class appliance
- You are okay operating a gateway (Postgres, backups, hardening)

Prefer a broader platform (for example Teleport) if you need first-class Kubernetes, database, Windows, and web app access in one product on day one. See our comparison: [Orion Belt vs Teleport](/blog/orion-belt-vs-teleport).

## Try the model in about ten minutes

The fastest way to feel the tradeoff is not a whitepaper:

```bash
git clone https://github.com/orion-belt-dev/orion-belt.git
cd orion-belt
./scripts/docker-quickstart.sh
```

Then follow [Try Orion Belt in 10 minutes](/docs/quickstart/try-in-10-minutes): register an agent, dial out, SSH through the gateway, open **Sessions** for playback.

## FAQ

**Is reverse SSH the same as `ssh -R`?**  
Conceptually related: the remote side opens a channel back toward you. Orion Belt productizes that idea with enrollment, policy, and recording — not a one-off shell tunnel.

**Do I still expose port 22 on the target?**  
Not for the Orion path. You may leave local SSH for break-glass; just know those sessions bypass gateway recording.

**What about lateral movement from a compromised gateway?**  
A compromised gateway is bad in *any* bastion design. Mitigate with MFA, least privilege, network allowlists, recording encryption, and host hardening — see the main repo’s deployment hardening docs.

## Next reading

- [Announcing Orion Belt](/blog/announcing-orion-belt)
- [Session recording is hard](/blog/session-recording-is-hard)
- [Architecture overview](/docs/architecture/overview)
- [Discord](https://discord.gg/w62S8jxTHJ) · [GitHub](https://github.com/orion-belt-dev/orion-belt)
