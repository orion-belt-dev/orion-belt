# RDP and VNC

RDP and VNC go through the same agent tunnel as SSH: the agent dials out to
the gateway, so the target needs no inbound port. The gateway relays the raw
TCP stream; the Windows or VNC login happens in your client, against the
target.

## Grants

RDP and VNC are access types of their own, `rdp` and `vnc`. A grant for one
covers that protocol only. `both` means ssh and scp, as it always has, and
never opens RDP or VNC. Tunnel grants name no remote user.

```bash
# Ask for access and wait for approval, then open the tunnel
osh rdp win-01 -r --reason "patch Tuesday" -d 1800
```

Admins grant from the console (Permissions, access type `rdp` or `vnc`) or
the API:

```bash
curl -X POST "$API/api/v1/admin/permissions" -H "Authorization: Bearer $KEY" \
  -d '{"user_id":"…","machine_id":"…","access_type":"rdp","duration_seconds":3600}'
```

## Connecting

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

## Agent configuration

By default an agent relays RDP to `127.0.0.1:3389` and VNC to
`127.0.0.1:5900` on its own host. Override or disable either in the agent
config:

```yaml
agent:
  name: win-01
  tunnels:
    rdp: 10.0.4.20:3389   # a desktop on the agent's network
    vnc: "off"            # refuse VNC on this agent
```

The gateway only names the protocol; the agent decides the address, so the
gateway cannot point an agent at anything its config does not list.

The agent does not yet build for Windows. To reach a Windows host today, run
the agent on a Linux machine on the same network and point `tunnels.rdp` at
the Windows host.

## Audit

Each connection writes `tunnel.open` and `tunnel.close` audit entries
(protocol, machine, bytes each way, duration), and a refused one writes
`tunnel.denied`. RDP and VNC sessions are not recorded yet: the stream is
encrypted end to end, so recording, live watch and clipboard control need the
gateway to terminate the protocol (#32, #33).

Agents older than this release refuse the tunnel. The gateway reports
"does not support rdp; upgrade it".
