---
sidebar_position: 11
title: RDP and VNC
---

# RDP and VNC

:::info[New in 1.4.0]
Needs the gateway, agents and `osh` on 1.4.0 or later.
:::

RDP and VNC go through the same agent tunnel as SSH. The agent dials out to
the gateway, so the target needs no inbound port. The gateway relays the raw
TCP stream; the Windows or VNC login happens in your client, against the
target.

---

## 1. Grants

RDP and VNC are access types of their own, `rdp` and `vnc`. A grant for one
covers that protocol only. `both` means `ssh` and `scp`, as it always has,
and never opens RDP or VNC. Tunnel grants name no remote user.

```bash
# Ask for access, wait for approval, then open the tunnel
osh rdp win-01 -r --reason "patch Tuesday" -d 1800
```

Admins grant from the console (**Permissions**, access type `rdp` or `vnc`),
with `oadmin`, or with the API:

```bash
oadmin permissions grant --user alice --machine win-01 --access-type rdp --duration 3600
```

---

## 2. Connect

```bash
osh rdp win-01                           # prints a local address, e.g. 127.0.0.1:53211
osh vnc lab-02 --listen 127.0.0.1:15900  # fixed local port
```

Point your client (mstsc, Remmina, FreeRDP, any VNC viewer) at the printed
address. Ctrl-C closes the tunnel. `osh` checks the grant before it opens the
local port, so a missing grant fails straight away with the reason.

Plain OpenSSH works too. Name the machine as the host and the protocol by its
port, 3389 for RDP or 5900 for VNC:

```bash
ssh -N -L 13389:win-01:3389 alice@orion-gateway.example.com
```

Other ports are refused.

---

## 3. Agent configuration

By default an agent relays RDP to `127.0.0.1:3389` and VNC to
`127.0.0.1:5900` on its own host. Override or disable either:

```yaml
agent:
  name: win-01
  tunnels:
    rdp: 10.0.4.20:3389   # a desktop on the agent's network
    vnc: "off"            # refuse VNC on this agent
```

The gateway only names the protocol; the agent decides the address, so the
gateway can never point an agent at anything its config does not list.

:::note
The agent does not build for Windows yet. To reach a Windows host today, run
the agent on a Linux machine on the same network and point `tunnels.rdp` at
the Windows host.
:::

---

## 4. Network interruptions

:::info[After 1.4.0]
Ships in the next release; not in 1.4.0.
:::

Tunnels are built to ride out flaky networks:

- **`osh` keeps the local port open.** If the gateway connection drops
  (sleep, Wi-Fi change, gateway restart), `osh` prints that it is
  reconnecting and redials with backoff (1 s up to 30 s). Your RDP or VNC
  client's own reconnect, or a new connection, goes through as soon as the
  gateway is back. A new connection waits up to a minute for it. A refused
  grant or a rejected key ends `osh` with the reason, since retrying cannot
  fix those.
- **Dead links are noticed in seconds, not minutes.** The gateway, agents and
  `osh` exchange keepalives every 15 seconds and close a connection that has
  not answered within 10, so a half-open link no longer hangs sessions until
  TCP gives up.
- **Agents reconnect at once.** An agent redials as soon as its connection
  drops, with jittered backoff. Its new connection replaces the old one on the
  gateway, so the machine is back online the moment the agent is.
- **Bursts of short connections are bounded.** One client connection can hold
  at most 128 tunnels and one agent 256 (extra ones are refused as a resource
  shortage). A relay whose peer stops sending but never closes is shut 30
  seconds after the other side finished. A permission check is reused for 5
  seconds on the same connection, so a revoked grant can take that long to
  stop new connections; open ones end when they close.

The RDP or VNC session itself does not survive the drop: the TCP stream to the
target is cut and the client reconnects, as it would on any network change.

---

## 5. Audit

Each connection writes `tunnel.open` and `tunnel.close` audit entries
(protocol, machine, bytes each way, duration); a refused one writes
`tunnel.denied`. Connections appear in the [compliance reports](./compliance-reports)
as `rdp_session` and `vnc_session` rows.

RDP and VNC sessions are not recorded yet: the stream is encrypted end to
end, so recording, live watch and clipboard control need the gateway to
terminate the protocol. Agents older than 1.4.0 refuse the tunnel, and the
gateway says to upgrade them.
