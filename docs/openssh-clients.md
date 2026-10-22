# OpenSSH agentless clients

Orion Belt accepts vanilla OpenSSH clients — no `osh` binary required.

## Interactive shell

```bash
# As root on machine web-01 (authenticate as gateway user alice)
ssh alice+web-01@orion-gateway.example.com

# As bob on web-01
ssh 'alice+bob%web-01@orion-gateway.example.com'
# or
ssh 'alice+bob@web-01@orion-gateway.example.com'
```

Username format: `gatewayUser+machine` or `gatewayUser+remoteUser%machine`.

## Exec / one-shot commands

When the username names a machine, the whole command runs on that machine:

```bash
ssh alice+web-01@orion-gateway.example.com uptime          # as root on web-01
ssh 'alice+bob%web-01@orion-gateway.example.com' uptime    # as bob on web-01
ssh -t alice+web-01@orion-gateway.example.com sudo -i      # with a terminal
```

Quotes, pipes and other shell syntax reach the target unchanged, for example
`ssh alice+web-01@gw "/bin/sh -c 'echo \"\$HOME\" | wc -c'"`.

Without a machine in the username, the first word of the command is the
target, as `[remoteUser@]machine`:

```bash
ssh alice@orion-gateway.example.com 'bob@web-01'
ssh alice@orion-gateway.example.com 'bob@web-01 uptime'
```

Both forms check the same grant, write the same audit entry and record the
session. A user without a grant for that remote account gets
"Permission denied" and exit status 1.

## FIDO / YubiKey SSH keys

Store your security-key public key (e.g. `sk-ssh-ed25519@openssh.com AAAA...`) as the user's primary key or via **Security → SSH keys** in the web UI / `POST /api/v1/ssh-keys`.

```bash
ssh-keygen -t ed25519-sk -f ~/.ssh/id_ed25519_sk
# paste id_ed25519_sk.pub into Orion
ssh -i ~/.ssh/id_ed25519_sk alice+web-01@orion-gateway
```

Touch the YubiKey when prompted by OpenSSH.

## Example `~/.ssh/config`

```sshconfig
Host orion
  HostName orion-gateway.example.com
  Port 2222
  User alice
  IdentityFile ~/.ssh/id_ed25519_sk

Host web-01
  HostName web-01
  ProxyCommand none
  # Route through Orion using the +machine username form:
Host web-01.orion
  HostName orion-gateway.example.com
  Port 2222
  User alice+web-01
  IdentityFile ~/.ssh/id_ed25519_sk
  RequestTTY force
```

Then: `ssh web-01.orion`

## Using Ansible and AWX

Ansible runs every task as a non-interactive SSH command, so it works through
the gateway once the machine and remote user are in the username. No wrapper
script is needed, and the managed servers need no inbound SSH.

### Service account and grants

Create a gateway account for automation and grant it the remote users it may
become on each machine:

```bash
orion-belt-server user create --name svc-ansible --email ansible@example.com \
  --key "$(cat ansible_ed25519.pub)"
orion-belt-server permission grant --user svc-ansible --machine web-01 --type ssh
```

Scope grants to the remote accounts the playbooks need (see
[SERVER-CLI.md](SERVER-CLI.md)). Every task is recorded and attributed to
`svc-ansible`.

### Inventory

```yaml
all:
  vars:
    ansible_host: orion-gateway.example.com
    ansible_port: 2222
    ansible_user: "svc-ansible+root@{{ inventory_hostname }}"
    ansible_ssh_private_key_file: ~/.ssh/ansible_ed25519
    # The gateway has no SFTP subsystem; send modules over stdin.
    ansible_ssh_transfer_method: piped
  hosts:
    web-01:
    db-01:
      ansible_user: "svc-ansible+postgres@db-01"
```

Separate the remote user and machine with `@` here, not `%`: Ansible passes
the user as `-o User=...`, and recent OpenSSH releases expand `%` tokens in
that value (`unknown key %w`). The same applies to `User` in `ssh_config`.

`ansible all -m ping` and playbooks then run as usual. Pin the gateway's host
key (or its Host CA, below) in `known_hosts` rather than turning off host key
checking.

### Recording volume

Each task opens at least one session per host, and without pipelining a task
uses several (create a temporary directory, run the module, clean up).
Enable pipelining to keep it to one where the targets allow it
(`pipelining = True` under `[ssh_connection]`, which needs `requiretty` off in
sudoers). Expect one recording per task per host; size recording storage and
retention for that.

### AWX / Ansible Automation Platform

- Credential: a **Machine** credential holding the service account's private
  key. Leave its username empty: `ansible_user` from the inventory, which
  names the machine, takes precedence.
- Inventory: set `ansible_host`, `ansible_port`, `ansible_user` and
  `ansible_ssh_transfer_method: piped` as above, as inventory or group
  variables.
- Execution environment: the default images already include the OpenSSH
  client. The only network access they need is to the gateway's SSH port.

SFTP transfer, per-job access requests instead of standing grants, and
grouping one playbook run into a single session view are not supported yet.

## Host CA trust (optional)

When the gateway runs with `ssh_ca.enabled`, it presents a Host-CA-signed certificate for its SSH host key. Cert-aware clients can pin the Host CA instead of TOFU:

1. `oadmin ca export` (or `GET /api/v1/admin/ca/export`) → copy `host_ca`
2. Set `auth.host_ca_public_key` in client config, **or** for raw OpenSSH:

```sshconfig
Host orion-gateway.example.com
  HostName orion-gateway.example.com
  Port 2222
  CertificateFile ~/.ssh/id_ed25519-cert.pub   # if using a User cert from POST /ssh-cert
  # Trust the Orion Host CA for this gateway:
  #   @cert-authority orion-gateway.example.com ssh-ed25519 AAAA...
  # (add that line to ~/.ssh/known_hosts)
```

See [SSH_CA.md](SSH_CA.md).

## Notes

- Agents still run on target hosts (reverse tunnel). “Agentless” refers to **clients**, not removing machine agents.
- ProxyJump / `direct-tcpip` is not used; Orion opens a session on the agent instead.
- SCP: prefer `ocp`, or `ssh alice@gw 'alice@web-01 scp -t /path'` style exec (same as `ocp`).
- Vanilla OpenSSH does not auto-fetch User certs — use `osh`/`ocp`/`oadmin` for that, or request a cert via `POST /api/v1/ssh-cert` and point `CertificateFile` at it.

## Lab verification

To exercise these flows on a local QEMU gateway, follow [E2E_TEST_PLAN.md](E2E_TEST_PLAN.md) cases **TC-QEMU-007** … **TC-QEMU-010** (`make lab-qemu-start`).
