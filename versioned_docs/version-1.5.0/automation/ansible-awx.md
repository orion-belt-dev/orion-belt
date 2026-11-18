---
sidebar_position: 10
title: Ansible and AWX
---

# Ansible and AWX

Ansible runs every task as a non-interactive SSH command. Since 1.3.0 the
gateway runs those commands on the machine named in the SSH username, so
Ansible and AWX manage servers through Orion Belt with no wrapper script and
no inbound SSH on the targets. Every task is authorized, audited and recorded.

## Service account and grants

Create a gateway account for automation and grant it the remote users it may
become on each machine:

```bash
orion-belt-server user create --name svc-ansible --email ansible@example.com \
  --key "$(cat ansible_ed25519.pub)"
orion-belt-server permission grant --user svc-ansible --machine web-01 --type ssh
```

Scope grants to the remote accounts the playbooks need. Sessions are attributed
to `svc-ansible`.

## Inventory

```yaml
all:
  vars:
    ansible_host: orion.example.com
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

Separate the remote user and machine with `@`, not `%`: Ansible passes the user
as `-o User=...`, and recent OpenSSH releases expand `%` tokens in that value
(`unknown key %w`).

`ansible all -m ping` and playbooks then run as usual. Pin the gateway's host
key, or its Host CA, in `known_hosts` rather than turning off host key checking.

## Recording volume

Each task opens at least one session per host. Without pipelining a task uses
several (create a temporary directory, run the module, clean up). Enable
pipelining to keep it to one where the targets allow it (`pipelining = True`
under `[ssh_connection]`, which needs `requiretty` off in sudoers). Plan
recording storage and retention for one recording per task per host.

## AWX / Ansible Automation Platform

- **Credential:** a Machine credential holding the service account's private
  key. Leave its username empty; `ansible_user` from the inventory, which names
  the machine, takes precedence.
- **Inventory:** set `ansible_host`, `ansible_port`, `ansible_user` and
  `ansible_ssh_transfer_method: piped` as inventory or group variables.
- **Execution environment:** the default images include the OpenSSH client.
  They only need network access to the gateway's SSH port.

## Not supported yet

- SFTP transfer (Ansible's default transfer method)
- Per-job access requests from AWX instead of standing grants
- Grouping one playbook run into a single session view
