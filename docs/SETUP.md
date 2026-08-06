# First-run setup

After installing the `orion-belt` package (or via
[`scripts/install-server.sh`](../scripts/install-server.sh)), follow this path.

## 1. Configure the server

```bash
sudoedit /etc/orion-belt/server.yaml
```

Set at least:

- `server.public_url` — origin browsers and API clients use (e.g. `https://orion.example.com`). **Not** the bind address. Agents default to this URL's host on port `2222` unless you set `public_ssh_host` / `public_ssh_port`.
- `database.connection_string` — Postgres DSN
- `auth.jwt_secret` — long random string (not the example value)

The install script and `orion-belt-server setup` both prompt for the public URL and write it for you.

This is enough to start the server, but it leaves every optional hardening
control at its default. Before going further, diff your config against
[`config/server.example.yaml`](../config/server.example.yaml) — it's the only
place these are documented:

- `auth.webauthn.*` — hardware-key (FIDO2/YubiKey) login; see [WebAuthn](#webauthn-fido2) below. When empty, `rp_id` / `origins` are filled from `server.public_url`.
- `auth.mfa_required` — require TOTP after SSH-key API login (`osh`/`ocp`/`oadmin`); off by default so device login (`osh login`) is key-only. Password login always requires TOTP regardless.
- `auth.rate_limit_per_minute` — per-user/IP request cap on protected API routes (default `600`)
- `auth.openfga.*` — optional ReBAC via an external OpenFGA server instead of the built-in permission tables
- `ssh_ca.enabled` / `ssh_ca.master_key` — internal SSH CA (user + host certs); see [SSH_CA.md](SSH_CA.md). `master_key` is required when enabled (encrypts CA keys at rest)
- `ssh_ca.host_principals` — hostnames/IPs clients use to reach the gateway (embedded in the gateway Host cert); often the same host as `public_url`
- `recording.encryption_key` — AES-256-GCM key for session recordings at rest; leave empty only if you accept plaintext recordings
- `recording.compression` — `gzip` (default) or `none` for cast files at flush
- `recording.retention_days` — how long recordings are kept before the retention loop deletes them

See also [DEPLOYMENT_HARDENING.md](DEPLOYMENT_HARDENING.md) and [OBSERVABILITY.md](OBSERVABILITY.md).

### WebAuthn (FIDO2)

Registration happens in the console (**Security → WebAuthn**) while signed in. Login only uses keys that are already registered.

Configure `auth.webauthn` in `server.yaml`, then restart the gateway:

```yaml
server:
  public_url: "https://orion.example.com"
auth:
  webauthn:
    enabled: true
    rp_display_name: "Orion Belt"
    rp_id: "orion.example.com"   # hostname only (no port) — match public_url
    origins:
      - "https://orion.example.com"
```

- **`rp_id`** must match the browser hostname (e.g. `orion.example.com`).
- Every UI origin you use must be listed under **`origins`**.
- After config, open **Security → WebAuthn → Register YubiKey / FIDO2**, then use **Security key** on the login page.

```bash
sudo systemctl enable --now orion-belt-server
# Alpine / OpenRC:
#   sudo rc-update add orion-belt-server default
#   sudo rc-service orion-belt-server start
```

UI: `<public_url>/ui` (or `http://<host>:8080/ui` only for local labs).

Confirm the running build (footer / workspace bar, or):

```bash
curl -s http://localhost:8080/api/v1/version
curl -s http://localhost:8080/api/v1/gateway-info   # advertised public URL / SSH host
orion-belt-server --version
```

OpenAPI: `http://<host>:8080/api/v1/openapi.yaml` — see [API/README.md](API/README.md).

## 2. Run the setup wizard

```bash
sudo -u orionbelt orion-belt-server -c /etc/orion-belt/server.yaml setup
```

Sets **public_url** (if missing), creates the first **admin** (if missing), and prints agent/user guidance.

Non-interactive:

```bash
export ORION_SETUP_PUBLIC_URL=https://orion.example.com
export ORION_SETUP_ADMIN_NAME=admin
export ORION_SETUP_ADMIN_EMAIL=admin@example.com
export ORION_SETUP_ADMIN_KEY_FILE=/path/to/admin.pub   # or YubiKey sk-*.pub
orion-belt-server -c /etc/orion-belt/server.yaml setup
```

## 3. Add agents

### Recommended — UI install script

1. Sign in as **admin** or **operator**.
2. Open **Add agent** in the console.
3. Choose the target OS (Debian/Ubuntu, RHEL/Rocky, openSUSE, Alpine, or generic Linux).
4. Set agent name, gateway host (SSH port **2222**), and **package base URL**. The UI defaults to the public mirror [`https://orion-belt-dev.github.io/packages`](https://orion-belt-dev.github.io/packages) and loads **Package version** from that mirror’s `VERSION` file. For a local `dist/` build instead: `make packages && make serve-packages`, then set the base URL to `http://127.0.0.1:8765` (or your host IP).
5. **Generate install script** — the server registers the agent and returns a root shell script that embeds the agent private key, downloads the package, writes `/etc/orion-belt/agent.yaml`, and starts the service.
6. Copy or download the script and run it on the target host as root.

API equivalent: `POST /api/v1/admin/agents/install-script` (see OpenAPI).

### Manual

On each target host:

1. Install `orion-belt-agent` (see [PACKAGING.md](PACKAGING.md) for apt/dnf/apk/Arch).
2. Edit `/etc/orion-belt/agent.yaml` — gateway host and port **2222**.
3. Generate a key (`ssh-keygen -t ed25519 -f /etc/orion-belt/agent_key -N ""`) and register the **public** key (`POST /api/v1/public/register/agent` or `orion-belt-server agent register`).
   - With **SSH CA** enabled, registration returns a Host certificate — write it to `/etc/orion-belt/agent_key-cert.pub` and set `auth.host_ca_public_key` from `oadmin ca export` (see [SSH_CA.md](SSH_CA.md)).
   - Without CA, registration creates a synthetic agent user (legacy path).
4. `systemctl enable --now orion-belt-agent`

Connected tunnels appear under **Agents**.

## 4. Users and grants

- UI **Users** — create operators / auditors / users
- Grant machine access with `remote_users` (e.g. `root`)
- CLI:

```bash
orion-belt-server user create --name alice --email a@x --key "$(cat alice.pub)"
orion-belt-server permission grant --user alice --machine web-01 --type both --remote-users root
```

## 5. Connect (everything is recorded)

OpenSSH through the gateway:

```bash
ssh -i alice.pem -p 2222 alice+web-01@gateway-host
```

Web **Terminal** in the UI also creates auditable sessions with timed cast recordings (PTY output).

Direct SSH to an agent host **bypasses** Orion (no recording). Point users at the gateway.

## UI checklist

Admins/operators see **Setup guide** and **Add agent** in the nav, plus a dashboard banner until agents are connected.

Admins/operators/auditors also get a live **Access analytics** card on the dashboard (rolling window selector) showing access volume, approval latency, and top targets without generating manual reports.

Web **Terminal** sessions are recorded (`source=web`, `.cast`) and show under **Sessions** with xterm playback (play/pause/seek). Full UI requirements: [SRS-UI.md](SRS-UI.md).
