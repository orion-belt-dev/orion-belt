---
sidebar_position: 15
title: Agent Remote Management
---

# Agent remote management

:::info[After 1.4.0]
Ships in the next release; not in 1.4.0. Needs the gateway, the agent and
`oadmin` on that release.
:::

Operate agents from the gateway, without SSHing to each target: check them,
reload their config, drain them for maintenance, and update their binary.
Every command goes over the agent's existing outbound connection, needs an
admin or operator, and is audited as `agent.command`.

| Command | Does |
| --- | --- |
| `oadmin agents command <agent> orion:status` | Health, plus whether it is draining and how many sessions run |
| `oadmin agents reload <agent>` | Re-read the agent's config file without restarting |
| `oadmin agents drain <agent> [--timeout 600]` | Stop new sessions, let running ones finish |
| `oadmin agents undrain <agent>` | Take new sessions again |
| `oadmin agents update <agent> --url … --sha256 …` | Install a new binary and restart |

The same commands go through `POST /api/v1/admin/agents/{machine_id}/command`
with `{"command": "orion:reload"}`, `orion:drain [seconds]`, `orion:undrain`
or `orion:update <url> <sha256> [drain-seconds]`.

---

## 1. Reload

The agent reads the file it was started with again and applies what can
change while it stays connected:

- `agent.tags`
- `agent.tunnels` (RDP/VNC relay addresses)
- `agent.kubernetes` connection settings (API server, token, CA)
- `agent.remote_update`

Changing the gateway address, credentials (`auth`), the agent name, or
turning Kubernetes on or off needs a restart: reload lists these under
`restart_required` and leaves them as they are. A file that does not parse
is refused and the running config kept.

---

## 2. Drain

While draining, the agent refuses new shells, commands, RDP/VNC tunnels and
kubectl sessions, and lets running ones continue. When they have all ended,
or the timeout passes (600 seconds by default), it closes whatever is left.
Control commands keep working, so you can check progress with
`orion:status` and end the drain with `undrain`. In-cluster agents keep
issuing and removing Kubernetes bindings while draining.

Refused tunnels are reported as temporary, so `osh rdp`, `osh vnc` and
`osh kube` keep retrying for up to a minute instead of giving up during a
short maintenance.

---

## 3. Update

```bash
oadmin agents update web-01 \
  --url https://github.com/orion-belt-dev/orion-belt/releases/download/v1.5.0/orion-belt-agent_1.5.0_linux_amd64.tar.gz \
  --sha256 <the archive's line in checksums.txt>
```

1. The agent downloads the URL (https only; a binary or a release
   `.tar.gz`, at most 512 MB) and checks its SHA-256. A mismatch stops here
   with nothing changed.
2. The binary is written next to the running one as `<binary>.new`.
3. The agent drains (`--drain-timeout`, 600 seconds by default), moves the
   current binary to `<binary>.old`, puts the new one in place and restarts
   through systemd, or exits so its process supervisor starts it again.

If the swap fails, the agent keeps its current binary and takes sessions
again. To roll back, restore `<binary>.old` and restart the service.

:::tip
Packaged installs (deb, rpm, apk) are better updated with the package
manager, which the next package upgrade would otherwise overwrite. In
containers, update the image instead: a restarted container starts from its
image again.
:::

Set `agent.remote_update: false` in an agent's config to refuse remote
updates on that agent.
