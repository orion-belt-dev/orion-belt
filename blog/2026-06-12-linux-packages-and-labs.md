---
slug: linux-packages-and-labs
title: Shipping deb/rpm/apk packages and a Docker lab for SSH PAM
description: Why Orion Belt ships native Linux packages and a Docker quickstart — two audiences for self-hosted SSH bastion installs, signed repos, and reproducible bug reports.
authors: [orion]
tags: [ops, self-hosted, linux]
keywords: [SSH bastion packages, self-hosted PAM Docker, deb rpm apk, Linux privileged access]
---

Real gateways want **systemd** and signed package repos. First-time evaluators want `docker compose` and a browser. Orion Belt ships both on purpose. This post explains the split for operators installing a **self-hosted SSH bastion / PAM** stack.

{/* truncate */}

## Two audiences, two artifacts

| Audience | Success looks like | Artifact |
| --- | --- | --- |
| Security / platform eng | Survives reboot, upgrades, SELinux | deb / rpm / apk + docs |
| Curious SRE on a laptop | Recorded session in 10 minutes | `docker-quickstart.sh` |

If you only ship packages, blog traffic bounces. If you only ship Docker, production teams bounce. Document them as **siblings**, not one confused path — see [10-minute install path](/blog/ten-minute-install-path).

## What “make packages” is for

From the product repo:

```bash
make packages
# optional signed repos with ORION_REQUIRE_SIGN=1
```

You get installable **server**, **agent**, and **tools** packages for common Linux distros. First-run after packages: server YAML, `setup`, Add agent — covered in [Server setup](/docs/quickstart/server) and the main repo `SETUP.md` / `PACKAGING.md`.

## What Docker is for

- Marketing conversion  
- Reproducing GitHub issues  
- Homelabs and CI smoke tests  
- Demoing reverse agents without cloud VMs  

When someone says “agent won’t connect,” asking for a Docker quickstart repro saves days of “what’s in your custom unit file?”

## Signed repos and supply chain

GPG-signed APT/RPM trees matter once you leave the lab. Treat packaging keys like release keys: generate, backup, publish the public key, and document fingerprint verification. The scripts in-tree (`gen-packaging-key.sh`, `publish-repos.sh`) exist so you are not inventing that ceremony under pressure.

## FAQ

**Can I run production on Docker Compose?**  
You can. Many teams still prefer packages + systemd for host networking and upgrade habits. Compose is not immoral; just own backup/restore for volumes.

**Do agents need the same package format as the server?**  
Match the target OS. The UI install script path is built around that.

## Related

- [Try in 10 minutes](/docs/quickstart/try-in-10-minutes)  
- [Lessons building PAM](/blog/lessons-building-self-hosted-pam)  
- [GitHub](https://github.com/orion-belt-dev/orion-belt)
