# First-run setup

This guide takes a new server from package installation to a first recorded
session. It assumes you installed the `orion-belt` package or ran
[`scripts/install-server.sh`](../scripts/install-server.sh).

## 1. Configure the server

```bash
sudoedit /etc/orion-belt/server.yaml
```

Set at least these values:

| Setting | Value |
|---|---|
| `server.public_url` | The origin browsers and API clients use, such as `https://orion.example.com`. This is not the bind address. Agents connect to this URL's host on port 2222 unless `public_ssh_host` or `public_ssh_port` say otherwise. |
| `database.connection_string` | PostgreSQL connection string. |
| `auth.jwt_secret` | A long random string, for example from `openssl rand -hex 32`. Do not keep the example value. |

The install script and `orion-belt-server setup` both ask for the public URL
and write it for you.

These settings are enough to start the server, but every optional security
control is still at its default. Compare your file with
[`config/server.example.yaml`](../config/server.example.yaml), which documents
each option. The ones to review before going live:

| Setting | Purpose |
|---|---|
| `server.trusted_proxies` | Reverse proxies allowed to report the client address. Required behind a proxy; see [REVERSE_PROXY.md](REVERSE_PROXY.md). |
| `auth.webauthn.*` | Hardware-key (FIDO2) login; see [WebAuthn](#webauthn-fido2). When `rp_id` and `origins` are empty they are derived from `server.public_url`. |
| `auth.mfa_required` | Require a TOTP code after SSH-key login from `osh`, `ocp` and `oadmin`. Off by default, so device login (`osh login`) uses the key alone. Password login always requires TOTP. |
| `auth.rate_limit_per_minute` | Request limit per user or IP address on authenticated API routes. Default 600. Sign-in and registration endpoints have a separate, fixed limit of 30 requests per minute per IP address. |
| `auth.openfga.*` | Use an external OpenFGA server for authorization instead of the built-in permission tables. |
| `ssh_ca.enabled`, `ssh_ca.master_key` | Built-in SSH certificate authority for user and host certificates; see [SSH_CA.md](SSH_CA.md). `master_key` encrypts the CA keys at rest and is required when the CA is enabled. |
| `ssh_ca.host_principals` | Hostnames and addresses clients use to reach the gateway, embedded in its host certificate. Usually the host from `public_url`. |
| `recording.encryption_key` | AES-256-GCM key for recordings at rest. Leave it empty only if plaintext recordings are acceptable. |
| `recording.compression` | `gzip` (default) or `none`. |
| `recording.retention_days` | How long recordings are kept before they are deleted. |

See also [DEPLOYMENT_HARDENING.md](DEPLOYMENT_HARDENING.md) and
[OBSERVABILITY.md](OBSERVABILITY.md).

### WebAuthn (FIDO2)

Security keys are registered in the console under **Security > WebAuthn**
while signed in. Login accepts only keys that are already registered.

Configure `auth.webauthn` in `server.yaml` and restart the gateway:

```yaml
server:
  public_url: "https://orion.example.com"
auth:
  webauthn:
    enabled: true
    rp_display_name: "Orion Belt"
    rp_id: "orion.example.com"   # hostname only, without a port; must match public_url
    origins:
      - "https://orion.example.com"
```

- `rp_id` must be the hostname the browser shows.
- Every origin the console is served from must be listed under `origins`.
- After restarting, register a key under **Security > WebAuthn**, then choose
  **Security key** on the login page.

### Start the service

```bash
sudo systemctl enable --now orion-belt-server
# Alpine (OpenRC):
#   sudo rc-update add orion-belt-server default
#   sudo rc-service orion-belt-server start
```

The console is at `<public_url>/ui`. Plain `http://<host>:8080/ui` is only
suitable for local labs; in production, terminate TLS in a reverse proxy as
described in [REVERSE_PROXY.md](REVERSE_PROXY.md).

To confirm the running build, check the console footer or run:

```bash
curl -s http://localhost:8080/api/v1/version
curl -s http://localhost:8080/api/v1/gateway-info   # advertised public URL and SSH host
orion-belt-server --version
```

The OpenAPI specification is served at `/api/v1/openapi.yaml`; see
[API/README.md](API/README.md).

## 2. Run the setup wizard

```bash
sudo -u orionbelt orion-belt-server -c /etc/orion-belt/server.yaml setup
```

The wizard sets `public_url` if it is missing, creates the first admin
account if none exists, and prints next steps for agents and users.

To run it without prompts:

```bash
export ORION_SETUP_PUBLIC_URL=https://orion.example.com
export ORION_SETUP_ADMIN_NAME=admin
export ORION_SETUP_ADMIN_EMAIL=admin@example.com
export ORION_SETUP_ADMIN_KEY_FILE=/path/to/admin.pub   # a YubiKey sk-*.pub also works
orion-belt-server -c /etc/orion-belt/server.yaml setup
```

### Creating the first admin over the API

On a new install with no accounts, `POST /api/v1/public/register/client` with
`"is_admin": true` creates the first admin without authentication. The check
and the insert happen in one transaction, so two simultaneous requests cannot
both succeed. Once any account exists, the endpoint requires an admin or
operator credential. Prefer the setup wizard, which does not expose this
window on the network.

## 3. Add agents

### Install script (recommended)

1. Sign in as an admin or operator.
2. Open **Add agent**.
3. Choose the target OS: Debian or Ubuntu, RHEL or Rocky, openSUSE, Alpine, or
   generic Linux.
4. Enter the agent name, the gateway host (SSH port 2222), and the package base
   URL. The console defaults to the public mirror at
   [`https://orion-belt-dev.github.io/packages`](https://orion-belt-dev.github.io/packages)
   and reads the package version from that mirror's `VERSION` file. To use a
   local `dist/` build, run `make packages && make serve-packages` and set the
   base URL to `http://127.0.0.1:8765` or your host's address.
5. Select **Generate install script**. The server registers the agent and
   returns a shell script that contains the agent's private key, downloads the
   package, writes `/etc/orion-belt/agent.yaml` and starts the service. Treat
   the script as a secret.
6. Run the script as root on the target host.

The API equivalent is `POST /api/v1/admin/agents/install-script`.

### Manual installation

On each target host:

1. Install `orion-belt-agent`; see [PACKAGING.md](PACKAGING.md) for apt, dnf,
   apk and Arch.
2. Edit `/etc/orion-belt/agent.yaml` and set the gateway host and port 2222.
3. Generate a key with `ssh-keygen -t ed25519 -f /etc/orion-belt/agent_key -N ""`
   and register its public half, either with
   `orion-belt-server agent register` on the gateway or with
   `POST /api/v1/public/register/agent` using an admin or operator API key or
   session.
   - With the SSH CA enabled, registration returns a host certificate. Save it
     as `/etc/orion-belt/agent_key-cert.pub` and set
     `auth.host_ca_public_key` from `oadmin ca export`; see
     [SSH_CA.md](SSH_CA.md).
   - Without the CA, registration creates a service account for the agent
     (the legacy path).
4. Run `systemctl enable --now orion-belt-agent`.

Connected agents appear under **Agents**.

## 4. Users and grants

There is no self-service signup. An admin or operator creates each account,
either in the console under **Users** or from the command line. Operators can
create operator, auditor and user accounts; only admins can create or modify
admins.

Access to a machine is granted per remote account through `remote_users`, for
example `root`:

```bash
orion-belt-server user create --name alice --email alice@example.com --key "$(cat alice.pub)"
orion-belt-server permission grant --user alice --machine web-01 --type both --remote-users root
```

## 5. Connect

Every session through the gateway is recorded. With OpenSSH:

```bash
ssh -i alice.pem -p 2222 alice+web-01@gateway-host
```

The console's **Terminal** also creates recorded sessions.

Connecting to an agent host directly, without the gateway, bypasses Orion Belt
and is not recorded. Point users at the gateway and restrict direct SSH access
to the targets.

## In the console

Admins and operators see **Setup guide** and **Add agent** in the navigation,
and a dashboard banner until the first agent connects.

Admins, operators and auditors also see an **Access analytics** card on the
dashboard with access volume, approval latency and most-used targets over a
selectable window.

Web terminal sessions are recorded as `.cast` files with `source=web` and can
be replayed under **Sessions**. The full UI requirements are in
[SRS-UI.md](SRS-UI.md).
