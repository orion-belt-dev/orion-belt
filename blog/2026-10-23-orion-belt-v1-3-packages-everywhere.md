---
slug: orion-belt-v1-3-packages-everywhere
title: Orion Belt 1.3 — signed packages for every Linux distribution, clients for every OS
description: Orion Belt 1.3 publishes signed apt, dnf, zypper, apk and pacman repositories at pkgs.orion-belt.dev across nine CPU architectures, clients for macOS, Windows and the BSDs, a one-line installer, an access capability catalog in the console, and gateway hardening.
date: 2026-10-23T12:00:00Z
authors: [orion]
tags: [ops, self-hosted, linux]
keywords: [Orion Belt 1.3, SSH gateway apt repository, PAM rpm package, apk SSH bastion, pacman SSH gateway, pkgs.orion-belt.dev, install SSH bastion Linux]
---

**Orion Belt 1.3.0** (October 22, with 1.3.1 the next day) makes installing
the gateway, the agent and the clients a package manager command on almost
any machine. Everything comes from signed repositories at
**[pkgs.orion-belt.dev](https://pkgs.orion-belt.dev)**, so upgrades arrive with
the rest of your system updates.

{/* truncate */}

## Install in one line

```bash
curl -fsSL https://pkgs.orion-belt.dev/install.sh | sh                # clients: osh, ocp, oadmin
curl -fsSL https://pkgs.orion-belt.dev/install.sh | sh -s -- agent    # agent (Linux)
curl -fsSL https://pkgs.orion-belt.dev/install.sh | sh -s -- server   # server (Linux)
```

```powershell
irm https://pkgs.orion-belt.dev/install.ps1 | iex                     # clients on Windows
```

On Linux the script adds the repository for your package manager and
installs from it. Every download is checked against the release's
`checksums.txt`, and its GPG signature is checked when `gpg` is installed. If
you'd rather not pipe to a shell, add the repository by hand; the
[Get started](/docs/quickstart/get-started) guide has the commands per OS.

## What's covered

| Platform | Package format | Architectures |
| --- | --- | --- |
| Debian, Ubuntu, Raspberry Pi OS | `.deb` (apt) | amd64, arm64, armhf, armel, i386, ppc64el, s390x, riscv64, loong64 |
| Fedora, RHEL, Rocky, Alma, Amazon Linux | `.rpm` (dnf/yum) | x86_64, aarch64, armv7hl, armv6hl, i386, ppc64le, s390x, riscv64, loongarch64 |
| openSUSE, SLES | `.rpm` (zypper) | as above |
| Alpine | `.apk` | x86_64, aarch64, armv7, armhf, x86, ppc64le, s390x, riscv64, loongarch64 |
| Arch Linux, Arch Linux ARM, Arch Linux 32 | pacman | x86_64, aarch64, armv7h, i686 |
| macOS | archive | amd64, arm64 (clients) |
| Windows | zip, scoop | amd64, arm64, 386 (clients) |
| FreeBSD, OpenBSD, NetBSD, DragonFly, illumos, Solaris | archive | clients |
| Containers (GHCR) | image | amd64, arm64, arm/v7, arm/v6, 386, ppc64le, s390x, riscv64 |

Three packages per Linux platform: `orion-belt` (gateway),
`orion-belt-agent` and `orion-belt-tools` (`osh`, `ocp`, `oadmin`). The
server and agent packages work with systemd, OpenRC, runit and SysV init.

## Why so many architectures

Agents run where your machines are, and that's not only x86 servers:
Raspberry Pis in a lab, ARM boards at the edge, s390x and ppc64le in older
data centers. The agent is a single Go binary with no runtime dependencies,
so building it for nine CPUs costs CI minutes, not maintenance.

## Signed end to end

- The apt, dnf, zypper and pacman repositories are signed with the release
  GPG key.
- `.deb` files carry an embedded signature and `.rpm` files a native one.
- Every release file has a detached `.sig`, and `checksums.txt` is signed.

The key fingerprint is on the front page of
[pkgs.orion-belt.dev](https://pkgs.orion-belt.dev); compare it before you trust
the repository.

## Also in 1.3

- **Capability catalog in the console** (#57), building on the access
  catalog from 1.2.1: users see what they can request.
- **CLI coverage** for the endpoints added since 1.0 (#64).
- **Gateway hardening**: plugin settings validate admin-set URLs, paths and
  mail headers; Slack `response_url` posts are restricted; users can only
  delete their own SSH keys.
- **Exec fixes**: one-shot commands (`ssh user+web-01@gateway uptime`) run on
  the machine named in the SSH username, no longer lose output, and no longer
  hang when the command fails. A shell's last output arrives before the
  session ends.

## FAQ

**Is there an apt repository for Orion Belt?**
Yes: `deb [signed-by=/usr/share/keyrings/orion-belt.gpg] https://pkgs.orion-belt.dev stable main`,
after downloading the key from `https://pkgs.orion-belt.dev/orion-belt.gpg`.

**Does the agent run on a Raspberry Pi?**
Yes. The `armhf` build runs on Raspberry Pi OS, and `arm64` on 64-bit Pi OS.

**Can I run the gateway in Docker instead?**
Yes. Server and agent images are on GHCR for eight architectures.

Release notes:
[v1.3.0](https://github.com/orion-belt-dev/orion-belt/releases/tag/v1.3.0) ·
[v1.3.1](https://github.com/orion-belt-dev/orion-belt/releases/tag/v1.3.1)
