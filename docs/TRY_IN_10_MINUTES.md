# Try Orion Belt in 10 minutes

One command gives you a running gateway, a machine to connect to, and a link
that signs you in. By the end you will have opened a shell through Orion Belt
and replayed the recording of it.

## What you need

- **Docker**, running — [Docker Desktop](https://docs.docker.com/get-docker/) on
  Mac/Windows, Docker Engine + Compose plugin on Linux
- A web browser
- ~10 minutes, most of it waiting for the first build

No Go, no Node, no config files to edit. If something is missing, the script
says so before it changes anything.

## 1. Start it

```bash
git clone https://github.com/orion-belt-dev/orion-belt.git
cd orion-belt
./scripts/docker-quickstart.sh
```

That's the whole setup. It generates its own secrets, starts the gateway,
creates your admin user, registers a demo machine, and finishes with:

```text
╔══════════════════════════════════════════════════════════════════╗
║  Orion Belt is ready                                             ║
╚══════════════════════════════════════════════════════════════════╝

  1. Sign in — open this link (single use, expires in 5 minutes):

       http://localhost:8080/ui/bootstrap?code=8R76KANQ5F

     You will be asked to create a password and scan a TOTP QR code.
     That becomes your normal sign-in from then on.

  2. Machine "lab-1" is already connected. Open **Machines**, click its
     web terminal, and run a few commands.

  3. Open **Sessions** and press Playback to replay what you just did.
```

## 2. Sign in

Open the link. The script also tries to open it for you, so your browser may
already be there.

Orion Belt then asks you to create a password (10+ characters) and scan a QR
code with an authenticator app. That is your normal sign-in from then on.

If the link expired or you need another one:

```bash
./bin/osh -c client.yaml login
```

<details>
<summary>Why a link instead of a username and password?</summary>

In Orion Belt an identity is an SSH key, not a password. The script generated
one for you (`admin-key`) and registered it as the first admin. The link is how
your browser gets to use that key: `osh` proves it holds the private key and the
gateway hands back a one-time code.

A browser cannot sign a challenge with a key file on your disk, so there is no
"paste your public key" box in the console — that is why the first sign-in comes
from the command line.
</details>

## 3. Open a shell on the demo machine

In the console: **Machines** → **lab-1** → open its web terminal. Run a few
commands, whatever you like:

```bash
whoami
ls /
```

`lab-1` is a small Linux container the script registered and connected for you,
so there is something real to connect to. It dialled **out** to the gateway —
nothing was opened for inbound SSH.

## 4. Replay what you did

**Sessions** → pick the session you just ran → **Playback**. You can also
**live watch** a session that is still open.

That is the whole product in one loop: access is brokered through the gateway,
and every session is recorded, without exposing the target's SSH port.

## Optional: the same thing from your terminal

```bash
./bin/osh -c client.yaml root@lab-1              # interactive shell
```

Plain OpenSSH works too, no Orion Belt client needed:

```bash
ssh -i admin-key -p 2222 admin@localhost 'root@lab-1 uptime'
```

Both are recorded and show up under **Sessions** exactly like the web terminal.
More forms in [openssh-clients.md](openssh-clients.md).

## Stop it

```bash
./scripts/docker-quickstart.sh --down
```

Containers stop and your data volumes are kept, so starting again picks up where
you left off. To throw the data away too:

```bash
docker compose -f docker-compose.server.yml --env-file .env.server \
  --profile demo down --volumes
```

## If something goes wrong

Re-running `./scripts/docker-quickstart.sh` is always safe — it skips whatever
is already done and prints a fresh sign-in link. If a lab is already running it
asks whether to stop and recreate the containers first; your data (users,
machines, recordings) lives in Docker volumes and is kept either way. Say yes
unless you have a reason not to — recreating is also what clears a leftover
container from an interrupted run.

| What you see | What to do |
| --- | --- |
| `port is already allocated` | Something else uses 8080 or 2222. Re-run with different ones: `ORION_API_PORT=18080 ORION_SSH_PORT=12222 ./scripts/docker-quickstart.sh` |
| "The gateway did not come up" | `docker compose -f docker-compose.server.yml --env-file .env.server logs server` |
| Sign-in link rejected | Links are single-use and last 5 minutes. Get another: `./bin/osh -c client.yaml login` |
| `bin/osh` was not built | Your OS/architecture isn't covered by the automatic build. Install [Go](https://go.dev/dl/) and run `make build-client` |
| **lab-1** shows as offline | `docker compose -f docker-compose.server.yml --env-file .env.server logs agent` |
| Docker isn't running | Start Docker Desktop (or `sudo systemctl start docker`) and re-run |

## Next: a real machine

The demo agent is a shortcut — it runs next to the gateway on the same Docker
network. To manage a machine you actually care about, run the agent **on that
machine** with [docker-compose.agent.yml](../docker-compose.agent.yml) (or a
package from [PACKAGING.md](PACKAGING.md)). It dials out to the gateway, so that
machine still needs no inbound SSH port.

Skip the demo machine entirely with `./scripts/docker-quickstart.sh --no-agent`.

Before you put this on a real network, read
[DEPLOYMENT_HARDENING.md](DEPLOYMENT_HARDENING.md) — this quick start is a lab:
it trusts the gateway host key on first use, and WebAuthn and MFA enforcement
are off. [SETUP.md](SETUP.md) covers turning those on.

**Early operators:** if you deploy this and have feedback, open a
[Discussion](https://github.com/orion-belt-dev/orion-belt/discussions) or an
issue — we are looking for the first labs and small teams running v1.0.
