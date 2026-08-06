# Client CLI flags (osh / ocp / oadmin)

Shared persistent flags (all three tools):

| Flag | Env | Purpose |
|------|-----|---------|
| `-c, --config` | `ORION_CONFIG` | Client YAML path (default `~/.orion-belt/client.yaml`) |
| `-u, --user` | `ORION_USER` | Gateway username |
| `--api-endpoint` | `ORION_API_ENDPOINT` | HTTP API base (no trailing `/api`) |
| `-i, --identity` | — | Private key path (overrides `auth.key_file`) |
| `-v, --verbose` | — | Debug logging |
| `--json` | — | JSON output where implemented |
| `--timeout` | — | HTTP / dial timeout (default 30s) |

SSH clients (`osh`, `ocp`) additionally:

| Flag | Purpose |
|------|---------|
| `--proxy` | Gateway SSH host (overrides `server.host`) |
| `--proxy-port` | Gateway SSH port |
| `--insecure` / `--no-host-key-check` | Skip host-key verify (`strict_host_key_checking=no`) |

### First run (no config yet)

If `~/.orion-belt/client.yaml` (or `-c` path) is missing, `osh` / `ocp` / `oadmin`
start an interactive wizard: host, SSH port, API URL, username, key path. It
checks that the private key parses and that the API answers (`/api/v1/version`
or `/health`). If the API is down you can abort or save anyway.

### `osh login`

```bash
osh login                 # SSH key auth → open browser with one-time code
osh login --code          # print code + URL instead (CI / headless)
osh login --password      # prompt for password + TOTP, then open browser
osh login --password --code
```

If the organization sets `auth.mfa_required`, `osh login` / `ocp` / `oadmin`
prompt for a TOTP code after the SSH key proof. Otherwise device login with an
SSH key alone is enough (password console login still always needs TOTP).

Password login requires an account password and enrolled TOTP (set once in the
console under Security, or via the post-login setup gate). For hardware keys on
SSH itself, use an `sk-*` identity (`-i ~/.ssh/id_ed25519_sk`); browser WebAuthn
stays a console login method.

### Examples

```bash
osh -c ./client.yaml -u admin --api-endpoint http://localhost:8080 login
osh -u alice login --password
osh -u alice --proxy bastion.example.com --proxy-port 2222 -i ~/.ssh/alice web-01
ocp -u alice --insecure ./file web-01:/tmp/file
oadmin -u admin --api-endpoint http://localhost:8080 requests list --json
```

## Commands

`osh` connects to machines and manages your own account. `ocp` copies files and
browses remote filesystems. `oadmin` covers the admin surface of the console.
Every listing command accepts `--json`.

### `osh` — connect and manage your account

| Command | Purpose |
|---------|---------|
| `osh [user@]machine` | Open a session through the gateway |
| `osh --list` | List machines you can reach |
| `osh --request-access [user@]machine --reason … [--duration secs]` | Raise a JIT access request |
| `osh login` | Sign in to the web console (see above) |
| `osh whoami` | Show the identity and role the gateway resolved |
| `osh requests list [--status …]` / `get <id>` | Track your own access requests |
| `osh keys list` / `add <name> --key-file …` / `remove <id>` | Manage your SSH public keys |
| `osh api-keys list` / `create <name> [--expires-in secs]` / `revoke <id>` / `delete <id>` | Manage automation credentials |
| `osh mfa status` / `enroll` / `disable` | Manage the TOTP second factor |
| `osh password set` / `clear` | Manage your console password |
| `osh webauthn list` / `remove <id>` | Review hardware security keys (enrollment is a console ceremony) |
| `osh notifications list [--unread]` / `read <id>` / `read --all` / `prefs` | Read notifications, set delivery preferences |

`osh api-keys create` prints the raw key once — capture it there. Enrolling MFA
prints backup codes once, for the same reason.

### `ocp` — copy and browse files

| Command | Purpose |
|---------|---------|
| `ocp source destination` | Copy over the SSH tunnel (`machine:/path` on either side) |
| `ocp ls machine:/path` | List a remote directory |
| `ocp mkdir machine:/path` | Create a remote directory |
| `ocp rm machine:/path [-f]` | Delete a remote path (audited) |

### `oadmin` — operate the gateway

| Command | Purpose |
|---------|---------|
| `oadmin requests list` / `approve <id>` / `reject <id>` | Review pending access requests |
| `oadmin users list` / `get` / `create` / `update` / `delete` | Manage accounts |
| `oadmin machines list` / `get` / `create` / `update` / `delete` | Manage target machines |
| `oadmin permissions list [--user\|--machine]` / `grant` / `update` / `revoke` | Manage standing access |
| `oadmin sessions list [--active]` / `get <id>` / `content <id>` | Inspect sessions, download recordings |
| `oadmin audit list [--limit N] [--action …]` | Read the audit log |
| `oadmin agents list` / `command` / `disconnect` / `install-script` | Operate connected agents |
| `oadmin plugins list` / `enable` / `disable` / `config` | Manage plugins |
| `oadmin notifications policy` / `set-policy` | Set the org-wide notification bounds |
| `oadmin notifications templates [--body]` / `set-template` / `reset-template` | Edit the copy recipients see per event |
| `oadmin ca export` / `list-certs` / `revoke <serial>` | Manage the SSH CA |
| `oadmin reports export <name>` | Export a compliance report |
| `oadmin usage [--window H]` | Access volume and approval latency |
| `oadmin setup status` | First-run checklist |
| `oadmin gateway-info` | Addresses the gateway advertises to clients and agents |
| `oadmin version` | CLI and gateway versions (also a reachability check) |

Commands that take a user or machine accept the name as well as the ID:
`oadmin permissions grant --user alice --machine web-01`.

### Keeping the CLI in step with the API

`pkg/cliparity` records, for every route registered in `pkg/api`, either the CLI
command that exposes it or the reason none does. Its tests fail when a new
endpoint lands without that decision being made, and when a mapping names a
command that no longer exists — so a new endpoint from any theme has to be
answered with either a command or a stated exemption.


