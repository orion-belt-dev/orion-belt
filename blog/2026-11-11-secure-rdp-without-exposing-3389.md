---
slug: secure-rdp-without-exposing-3389
title: Secure RDP access without exposing port 3389 — VPN, RD Gateway and reverse tunnels compared
description: Exposing RDP on port 3389 to the internet invites brute force and exploits. Compare the alternatives — VPN, Microsoft RD Gateway, SSH tunnels and an outbound reverse tunnel — and see how Orion Belt 1.4 brokers RDP and VNC with approvals and audit, without inbound ports.
authors: [orion]
tags: [rdp, security, pam]
keywords: [secure RDP access, RDP without port 3389, RDP gateway alternative, RDP over SSH tunnel, VNC remote access security, just-in-time RDP access]
---

**The safest way to offer RDP is to not listen for it on the internet at
all.** An RDP port open to the world gets scanned and brute-forced
continuously, and RDP has had remote code execution bugs severe enough to be
wormable (BlueKeep,
[CVE-2019-0708](https://nvd.nist.gov/vuln/detail/CVE-2019-0708)). This post
compares the common ways to put something in front of RDP and VNC, and where
Orion Belt 1.4 fits.

{/* truncate */}

## The options

| Approach | Inbound port on the target network | Per-machine access | Approval / time limit | Audit of who connected |
| --- | --- | --- | --- | --- |
| RDP open to the internet | 3389 on every host | Windows login only | No | Windows event log |
| VPN, then RDP | VPN port | Network-wide | No | VPN logs + event log |
| Microsoft RD Gateway | 443 on the gateway | Resource policies | No | Gateway logs |
| SSH tunnel through a bastion | 22 on the bastion | What the bastion allows | No | Bastion logs |
| **Outbound reverse tunnel (Orion Belt)** | **None** | **Grant per machine** | **Yes** | **Gateway audit log** |

### VPN

Puts users on the network, then they RDP directly. Simple, but access is
network-wide: anyone on the VPN can try RDP against anything they can route
to. Fine for small teams; weak least privilege.

### RD Gateway

Microsoft's
[Remote Desktop Gateway](https://learn.microsoft.com/en-us/windows-server/remote/remote-desktop-services/remote-desktop-gateway-role)
wraps RDP in HTTPS and applies connection and resource authorization
policies. It's the standard choice in Windows-centric shops. It still needs
an inbound HTTPS endpoint, a Windows Server to run it, and has no
request-and-approve flow.

### SSH tunnel through a bastion

`ssh -L 13389:win-01:3389 bastion` works anywhere OpenSSH does. The bastion
needs to reach the Windows host, and nothing records who opened which tunnel
beyond sshd logs.

### Outbound reverse tunnel

An agent on the target network dials **out** to a gateway. Users connect to
the gateway, which checks their grant and relays the stream through the
agent's existing connection. The target network opens no inbound port at all.

## How it works in Orion Belt 1.4

RDP and VNC are access types of their own, `rdp` and `vnc`. A grant for one
covers that protocol only, and like SSH access it can be requested just in
time:

```bash
osh rdp win-01 -r --reason "patch Tuesday" -d 1800   # request, wait for approval
osh rdp win-01                                       # prints e.g. 127.0.0.1:53211
```

Point mstsc, Remmina, FreeRDP or any VNC viewer at the printed address.
Plain OpenSSH works too:

```bash
ssh -N -L 13389:win-01:3389 alice@orion-gateway.example.com
```

**The agent decides where traffic goes.** By default it relays RDP to
`127.0.0.1:3389` and VNC to `127.0.0.1:5900` on its own host. Its config can
point a protocol at another address on its network, or turn it off:

```yaml
agent:
  name: win-01
  tunnels:
    rdp: 10.0.4.20:3389   # a Windows desktop next to the agent
    vnc: "off"
```

The gateway only names the protocol, so it can never steer an agent at an
address its config doesn't list.

**Windows hosts today:** the agent doesn't build for Windows yet. Run it on a
Linux machine on the same network and point `tunnels.rdp` at the Windows
host.

## What gets audited

Each connection writes `tunnel.open` and `tunnel.close` entries (protocol,
machine, bytes each way, duration); refused attempts write `tunnel.denied`.
They appear in the [compliance reports](/docs/guides/compliance-reports) as
`rdp_session` and `vnc_session` rows.

RDP and VNC sessions are **not recorded** yet. The stream is encrypted end
to end between your client and the target; recording it would need the
gateway to terminate the protocol. Keep that in mind if screen recording is a
hard requirement.

## Hardening RDP itself

Whatever is in front, keep these on:

- **Network Level Authentication (NLA)**, so authentication happens before a
  full session is set up.
- **Account lockout** for repeated failures.
- **Firewall rules** that accept 3389 only from the relay (here, the agent's
  host, or `127.0.0.1` when the agent runs on the target).
- **Patching** on the RDP stack.

## FAQ

**Is it safe to expose RDP to the internet?**
No. Put a gateway, VPN or tunnel in front and accept RDP only from it.

**Can I use RDP through an SSH tunnel?**
Yes: forward a local port to the host's 3389 through an SSH server that can
reach it. Orion Belt accepts exactly that with plain `ssh -L`, and checks the
user's `rdp` grant first.

**Does this work for VNC?**
Yes, the same way, on port 5900 with the `vnc` access type.

## Related

- [Orion Belt v1.4](/blog/orion-belt-v1-4)
- [RDP and VNC guide](/docs/guides/rdp-vnc)
- [Why reverse SSH agents](/blog/why-reverse-ssh-agents)
