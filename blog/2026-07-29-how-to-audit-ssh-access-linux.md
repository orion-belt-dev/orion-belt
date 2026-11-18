---
slug: how-to-audit-ssh-access-linux
title: How to audit SSH access on Linux — what sshd, auditd, tlog and a gateway each record
description: A practical comparison of the ways to audit SSH sessions on Linux — sshd logs, auditd, pam_tty_audit, tlog, script and an SSH gateway — with what each one captures, what it misses, and how to combine them.
authors: [orion]
tags: [ssh, security, linux]
keywords: [audit SSH sessions, SSH audit log, record SSH sessions Linux, auditd SSH, tlog, pam_tty_audit, SSH session recording]
---

**To audit SSH access on Linux you need three things: who logged in, what
they did, and proof that the record wasn't changed by the person being
audited.** No single host tool gives you all three. This guide compares the
common options and shows where a gateway like Orion Belt fits.

{/* truncate */}

## The short answer

| Tool | Who logged in | What they typed | What they saw | Survives root on the host |
| --- | :---: | :---: | :---: | :---: |
| `sshd` logs (`LogLevel VERBOSE`) | Yes, with key fingerprint | No | No | No |
| `auditd` (execve rules) | Yes (auid) | Commands run, not keystrokes | No | Only if shipped off-host at once |
| `pam_tty_audit` | Yes | Keystrokes | No | Only if shipped off-host at once |
| `tlog` | Yes | Yes | Yes | Only if shipped off-host at once |
| `script` / shell history | Self-reported | Partly | `script` only | No |
| SSH gateway recording | Yes | Yes | Yes | **Yes**: recorded before the host |

The last column is the one auditors care about. Anything recorded on the
target can be edited by whoever has root on the target, and the people you
most need to audit usually do.

## sshd logs: the baseline

Set `LogLevel VERBOSE` in `/etc/ssh/sshd_config`. sshd then logs the
fingerprint of the key used for each login, not only the username, so you
can tell which of a shared account's keys was used
([sshd_config(5)](https://man.openbsd.org/sshd_config#LogLevel)).

It tells you nothing about what happened after login.

## auditd: commands, not sessions

The Linux audit framework can log every `execve` call with the login UID
(`auid`), which survives `sudo`:

```bash
-a always,exit -F arch=b64 -S execve -F auid>=1000 -F auid!=unset -k user-commands
```

You get a reliable list of programs run and their arguments. You do not get
keystrokes inside interactive programs (`vim`, `psql`, `mysql`) or anything
the user saw on screen. Volume is high; plan for log shipping.

## pam_tty_audit: keystrokes

`pam_tty_audit` records TTY input into the audit log
([pam_tty_audit(8)](https://man7.org/linux/man-pages/man8/pam_tty_audit.8.html)).
It catches keystrokes in interactive programs. By default it skips input
typed with echo off, such as passwords; the `log_passwd` option records that
too. Output is not recorded, so a reviewer sees what was typed but not what the system
answered.

## tlog: full terminal recording on the host

[tlog](https://github.com/Scribery/tlog) records terminal input and output
as JSON and can replay it. It's the most complete host-side option and is
packaged on RHEL and Fedora. Like everything on this list so far, it runs on
the target, under the target's root.

## script and shell history: not evidence

`script` and `~/.bash_history` are useful for the user. They are written by
the user, can be turned off by the user, and prove nothing.

## Recording at the gateway

An SSH gateway sits between the user and the host and records the session
stream as it passes through. The target never holds the recording, so root on
the target can't alter it.

In Orion Belt the gateway records every SSH session, optionally compressed
and encrypted at rest with a retention period. Admins can
[watch a session live](/blog/live-session-watch) and replay it in the
console. Each session is tied to the user's identity at the gateway, not to
the shared Unix account on the host, and to the approval that allowed it if
access was [requested just in time](/blog/jit-access-approvals).

Targets also don't need an inbound SSH port: the
[agent dials out](/blog/why-reverse-ssh-agents) to the gateway.

## A layered setup that holds up

1. **Gateway recording** for every interactive session: the tamper-resistant
   record of who did what.
2. **auditd** on hosts, shipped off-host, for commands run by services,
   cron jobs and anything that didn't come through the gateway.
3. **sshd `LogLevel VERBOSE`**, to spot direct logins that bypassed the
   gateway. Once the gateway is in place, there shouldn't be any.

## FAQ

**Can I record SSH sessions without installing anything on the server?**
Yes, at a gateway. Host-side tools (tlog, pam_tty_audit) need packages and
configuration on every server.

**Does auditd record what a user typed in vim or psql?**
No. auditd records the programs started (`execve`). Keystrokes need
`pam_tty_audit` or full session recording.

**Where should SSH audit logs be stored?**
Off the host, as soon as they're written, somewhere the audited users can't
write to. A gateway does this by design.

## Related

- [Why session recording is hard](/blog/session-recording-is-hard)
- [SSH bastion vs PAM](/blog/bastion-vs-pam)
- [Try Orion Belt in 10 minutes](/docs/quickstart/try-in-10-minutes)
