---
sidebar_position: 9
title: Troubleshooting
---

# Troubleshooting

## Agent won’t come online

Agents dial **out** to the gateway. Fix outbound path first.

1. Is the agent process running?

```bash
sudo systemctl status orion-belt-agent
# Docker lab:
docker compose -f docker-compose.agent.yml --env-file .env.agent logs agent
```

2. Can the agent reach **gateway:2222**? (not “can the gateway ping the agent”)
3. Does `ORION_AGENT_NAME` / registered name match?
4. Is `agent-key` the **private** key from **Add agent** (`chmod 600`)?

```bash
# gateway logs (Docker)
docker compose -f docker-compose.server.yml --env-file .env.server logs server
```

## Can’t sign in to `/ui`

- After docker-quickstart, open the **bootstrap link** the script prints (or run `./bin/osh -c client.yaml login` for a fresh one). Links are single-use and expire in 5 minutes.
- First visit sets a password + TOTP; later visits use that password (and TOTP if required).
- Confirm admin was created (`setup` / docker-quickstart).
- WebAuthn: `ORION_PUBLIC_HOST` / `ORION_PUBLIC_ORIGIN` must match the browser URL

## SSH / `osh` connect fails

| Symptom | Check |
| --- | --- |
| Auth failed | User’s public key registered; correct `-i` private key; port **2222** |
| No machines / denied | Permission grant + allowed remote user |
| Timeout | Operator → gateway network; gateway listening on 2222 |

```bash
ssh -vvv -p 2222 alice+web-01@gateway-host
curl -sf http://gateway-host:8080/health
```

## No session recording

- Session must go through the gateway (not direct SSH to the host)
- `recording.enabled` true
- Look under **Sessions** in the UI (web terminal sessions included)

## Server won’t start

- Postgres reachable; DSN / `POSTGRES_PASSWORD` correct
- `ORION_JWT_SECRET` / `auth.jwt_secret` set
- Ports 2222 / 8080 free

```bash
sudo systemctl status orion-belt-server
orion-belt-server --version
```

## Still stuck

- [Try in 10 minutes](../quickstart/try-in-10-minutes) on a clean machine
- [Discord](https://discord.gg/w62S8jxTHJ) · [GitHub issues](https://github.com/orion-belt-dev/orion-belt/issues)
