# SSH certificate authority

Orion Belt can run an internal SSH certificate authority. It issues
short-lived user certificates to people, and host certificates to the gateway
and to agents.

Enable it in `server.yaml`:

```yaml
ssh_ca:
  enabled: true
  master_key: "<32-byte secret, raw or base64>"  # required; encrypts the CA keys at rest
  user_cert_ttl_hours: 12
  max_user_cert_ttl_hours: 24
  host_cert_ttl_hours: 8760   # one year; renewed automatically before expiry
  host_principals:            # hostnames and addresses clients use to reach the gateway
    - orion.example.com
```

See [`config/server.example.yaml`](../config/server.example.yaml) for all
options. When the CA is first enabled, the gateway generates a user CA and a
host CA key pair and stores them, encrypted, in the `ssh_ca_keys` table. Every
issued certificate is recorded in `ssh_certificates`.

## User certificates

1. Export the user CA public key, and the host CA public key for clients that
   verify the gateway:

   ```bash
   oadmin ca export
   # or GET /api/v1/admin/ca/export
   ```

2. `osh`, `ocp` and `oadmin` detect the CA through `GET /api/v1/ssh-cert/ca`,
   request a certificate with `POST /api/v1/ssh-cert`, cache it, and renew it
   once less than 20% of its lifetime remains.

3. HTTP login still requires proof of possession: the client signs a
   server-issued challenge (`POST /api/v1/public/auth/challenge`) with its
   key, so knowing a user's public key is not enough to sign in.

4. Plain public-key authentication keeps working when the CA is off, and for
   users who have not moved to certificates while it is on.

## Gateway host certificate

With the CA enabled, the gateway presents a host certificate for its SSH host
key alongside the plain key. Clients that support certificates verify it
against `auth.host_ca_public_key` (see `config/client.example.yaml` and
`config/agent.example.yaml`) instead of trusting the key on first use.

The gateway renews its host certificate before it expires and applies the new
one to subsequent connections.

## Agent identity

With the CA enabled, registering an agent issues a host certificate for the
agent's key instead of creating a service account. Agents can be registered
through:

- the console's install script, or `POST /api/v1/admin/agents/install-script`;
- `POST /api/v1/public/register/agent`, which requires an admin or operator
  credential;
- `orion-belt-server agent register` on the gateway.

Each writes `<key_file>-cert.pub` and sets `auth.host_ca_public_key` in the
agent's configuration. The agent authenticates with the certificate, and the
gateway identifies it by machine. Agents registered before the CA was enabled
keep connecting with their service account until they receive a certificate.

### Renewal

When its cached host certificate enters the renewal window, the agent sends
the SSH global request `orion-renew-cert@orionbelt` with its public key. The
gateway replies with a new certificate, which the agent writes atomically and
uses on its next connection.

## Revocation

```bash
oadmin ca list-certs [--type user|host]
oadmin ca revoke <serial> [--reason "..."]
# or GET /api/v1/admin/ssh-certificates and POST /api/v1/admin/ssh-certificates/:serial/revoke
```

A revocation takes effect immediately on the gateway process that handled it.
Other gateway processes pick it up within 30 seconds.

## Migrating

| State | Behavior |
|-------|----------|
| CA disabled | Public-key authentication; agents use service accounts. |
| CA enabled, existing agent | Keeps connecting with its service account. |
| CA enabled, new agent | Host certificate only. Place `agent_key-cert.pub` next to the private key. |

Change `ssh_ca.master_key` only as part of a planned CA key rotation. If the
key is lost, the encrypted CA private keys cannot be recovered.
