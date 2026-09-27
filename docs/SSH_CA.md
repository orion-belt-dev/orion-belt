# SSH Certificate Authority

Orion Belt can act as an internal **SSH CA**: short-lived **User** certificates for operators, and **Host** certificates for the gateway and agents.

Enable with:

```yaml
ssh_ca:
  enabled: true
  master_key: "<32-byte secret, raw or base64>"  # required — encrypts CA keys at rest
  user_cert_ttl_hours: 12
  max_user_cert_ttl_hours: 24
  host_cert_ttl_hours: 8760   # 1y; auto-renewed before expiry
  host_principals:            # hostnames/IPs clients use to reach the gateway
    - orion.example.com
```

See `config/server.example.yaml`. On first enable, User + Host CA keypairs are generated and stored encrypted in Postgres (`ssh_ca_keys`). Issued certs are recorded in `ssh_certificates`.

## Operator (user) certs

1. Export the User CA pubkey (and Host CA for clients that verify the gateway):

   ```bash
   oadmin ca export
   # or GET /api/v1/admin/ca/export
   ```

2. Clients (`osh` / `ocp` / `oadmin`) auto-detect CA via `GET /api/v1/ssh-cert/ca`, request a user cert (`POST /api/v1/ssh-cert`), cache it, and renew when within 20% of TTL remaining (`pkg/ca.NeedsRenewal`).

3. HTTP login also requires **challenge-response** proof-of-possession (`POST /api/v1/auth/challenge` + signed login) so a stolen pubkey string alone is not enough.

4. Legacy raw-pubkey SSH/API auth still works when CA is off, and for users that have not migrated while CA is on (dispatcher in `pkg/server`).

## Gateway host cert

When CA is enabled, the gateway presents a Host-CA-signed cert for its SSH host key alongside the raw key. Cert-aware clients verify against `auth.host_ca_public_key` (see `config/client.example.yaml` / `config/agent.example.yaml`) instead of TOFU.

A background loop renews the gateway Host cert before TTL expiry and swaps `ssh.ServerConfig` for new connections.

## Agent identity (Host cert)

With CA enabled, agent registration **does not** create a synthetic user row:

- UI install script / `POST /api/v1/admin/agents/install-script`
- `POST /api/v1/public/register/agent` (admin/operator credential required)
- `orion-belt-server agent register`

…issue a Host cert for the agent’s pubkey, write `<key_file>-cert.pub`, and set `auth.host_ca_public_key` on the agent.

The agent authenticates with that cert; the gateway uses `handleAgentCertAuth` and routes by machine. Legacy synthetic-user agents still connect when no cert is present.

### Auto-renewal

Agents send SSH global request `orion-renew-cert@orionbelt` (payload = agent pubkey) when the cached Host cert enters the renewal window. The server replies with a fresh authorized_keys cert line; the agent writes it atomically and uses it on the next reconnect.

## Revocation

```bash
oadmin ca list-certs [--type user|host]
oadmin ca revoke <serial> [--reason "..."]
# or GET/POST /api/v1/admin/ssh-certificates[/:serial/revoke]
```

Revocation updates the in-memory cache immediately on that process. Other processes refresh every 30s (`runCARevocationRefreshLoop`).

## Migration notes

| Phase | Behavior |
|-------|----------|
| CA off | Unchanged pubkey / synthetic-agent flow |
| CA on, old agent | Still connects with synthetic user if registered that way |
| CA on, new agent | Host cert only; place `agent_key-cert.pub` next to the private key |

Rotate `ssh_ca.master_key` only with a deliberate CA key rotation procedure (not covered here) — losing it makes existing encrypted CA private keys unusable.
