# Packaging & installation

Every `v*` tag builds, packages, signs and publishes Orion Belt with
[GoReleaser](https://goreleaser.com) and nFPM (`.goreleaser.yaml`, run by
`.github/workflows/release.yml`), then refreshes the signed package
repositories at **<https://pkgs.orion-belt.dev>**.

## Install

```bash
curl -fsSL https://pkgs.orion-belt.dev/install.sh | sh                # clients: osh, ocp, oadmin
curl -fsSL https://pkgs.orion-belt.dev/install.sh | sh -s -- agent    # agent (Linux)
curl -fsSL https://pkgs.orion-belt.dev/install.sh | sh -s -- server   # server (Linux)
```

```powershell
irm https://pkgs.orion-belt.dev/install.ps1 | iex                     # clients on Windows
scoop install https://pkgs.orion-belt.dev/scoop/orion-belt.json       # or with scoop
```

On Linux, `install.sh` adds the repository for the system's package manager
(apt, dnf/yum, zypper, apk, pacman) and installs from it, so upgrades arrive
with the system's. Without a matching repository it installs the release
package, or the release archive under `/usr/local` (`--method binary`). On
macOS, the BSDs, illumos and Solaris it installs the clients. Every download is
checked against the release's `checksums.txt`, whose GPG signature is checked
when `gpg` is installed. `--help` lists the options (`--version`, `--method`,
`--prefix`, `--uninstall`).

The repositories by hand:

| Distribution | Setup |
|--------------|-------|
| Debian, Ubuntu, Raspberry Pi OS | `curl -fsSLo /usr/share/keyrings/orion-belt.gpg https://pkgs.orion-belt.dev/orion-belt.gpg`<br>`deb [signed-by=/usr/share/keyrings/orion-belt.gpg] https://pkgs.orion-belt.dev stable main` |
| Fedora, RHEL, Rocky, Alma, Amazon Linux | `curl -fsSLo /etc/yum.repos.d/orion-belt.repo https://pkgs.orion-belt.dev/orion-belt.repo` |
| openSUSE, SLES | `zypper addrepo https://pkgs.orion-belt.dev/orion-belt.repo` |
| Alpine | key `https://pkgs.orion-belt.dev/apk/orion-belt.rsa.pub` in `/etc/apk/keys/`, then `https://pkgs.orion-belt.dev/apk` in `/etc/apk/repositories` |
| Arch Linux, Arch Linux ARM, Arch Linux 32 | `pacman-key --add` the key and `--lsign-key` its fingerprint, then `[orion-belt]` with `Server = https://pkgs.orion-belt.dev/archlinux/$arch` |

The site's front page lists the same commands with the key fingerprint.

## What a release contains

| Package | Contents |
|---------|----------|
| `orion-belt` | gateway server, `/etc/orion-belt/server.yaml`, service files |
| `orion-belt-agent` | reverse-SSH agent, `/etc/orion-belt/agent.yaml`, service files |
| `orion-belt-tools` | `osh`, `ocp`, `oadmin` |

| Platform | Architectures | Files |
|----------|---------------|-------|
| Debian family (`.deb`) | amd64, arm64, armhf, armel, i386, ppc64el, s390x, riscv64, loong64 | all three packages |
| RPM family (`.rpm`) | x86_64, aarch64, armv7hl, armv6hl, i386, ppc64le, s390x, riscv64, loongarch64 | all three packages |
| Alpine (`.apk`) | x86_64, aarch64, armv7, armhf, x86, ppc64le, s390x, riscv64, loongarch64 | all three packages |
| Arch (`.pkg.tar.zst`) | x86_64, aarch64, armv7h, i686 | all three packages |
| Linux archives | the nine CPUs above | server + agent, agent alone, clients |
| macOS | amd64, arm64 | clients |
| Windows (`.zip`) | amd64, arm64, 386 | clients |
| FreeBSD | amd64, arm64, 386, armv7, riscv64 | clients |
| OpenBSD, NetBSD | amd64, arm64, 386 | clients |
| DragonFly, illumos, Solaris | amd64 | clients |
| Containers (GHCR) | amd64, arm64, arm/v7, arm/v6, 386, ppc64le, s390x, riscv64 | server, agent images |

32-bit ARM comes in three builds because distributions disagree about it:
the ARMv6 build is Debian `armhf` (it runs on Raspberry Pi OS as well as
Debian's ARMv7 `armhf`), rpm `armv6hl`, apk `armhf` and Arch `armv6h`; the
ARMv7 build is rpm `armv7hl`, apk `armv7` and Arch `armv7h`; the ARMv5 build is
Debian `armel`.

Signatures:

- `checksums.txt`: sha256 of every file, with a detached GPG signature `checksums.txt.sig`.
- Every package and archive has a detached binary GPG signature `<file>.sig`
  (pacman checks package `.sig` files of this form).
- `.deb` files carry an embedded signature (`_gpgorigin`); `.rpm` files a native
  RPM signature (`rpm -K`); `.apk` files an RSA signature when
  `APK_RSA_PRIVATE_KEY` is set.
- `orion-belt-release-key.asc`: the public key.

## Services

The server and agent packages work with **systemd, OpenRC, runit and SysV
init**. A helper, `/usr/lib/orion-belt/orion-belt-server-service` (or
`orion-belt-agent-service`), detects the init system and installs the matching
file from `/usr/share/orion-belt/init/`
(the systemd unit ships in `/lib/systemd/system`, or `/usr/lib/systemd/system`
on Arch):

| Init | File | Enabled with |
|------|------|--------------|
| systemd | `orion-belt-<role>.service` | `systemctl enable` |
| OpenRC | `/etc/init.d/orion-belt-<role>` | `rc-update add … default` |
| runit | `/etc/sv/orion-belt-<role>/run` (or `/etc/runit/sv`) | link in the runsvdir |
| SysV | `/etc/init.d/orion-belt-<role>` | `update-rc.d` / `chkconfig` |

```bash
/usr/lib/orion-belt/orion-belt-server-service setup     # install, enable, start
/usr/lib/orion-belt/orion-belt-agent-service status
/usr/lib/orion-belt/orion-belt-agent-service teardown   # stop, disable, remove
```

A fresh install does not start either service: the server needs its database
settings and the agent its gateway and key first. Upgrades restart a running
service; removal stops and removes it. Configuration, recordings and logs are
kept. The console's agent install script runs `orion-belt-agent-service setup`
itself.

## Agent install scripts (console)

**Agents → Add agent** (or `POST /api/v1/admin/agents/install-script`)
generates a script that picks the package for the host's CPU from the
**package base URL**: `https://pkgs.orion-belt.dev` by default, or any mirror
of a release's files, such as a local `dist/`:

```bash
make packages
make serve-packages   # http://127.0.0.1:8765, then use it as Package base URL
```

## Building locally

```bash
make packages            # goreleaser snapshot when installed: every package in dist/
make repos               # the package site in packages-site/ (unsigned unless ORION_GPG_KEY is set)
```

`make repos` needs `dpkg-scanpackages` and `apt-ftparchive` (dpkg-dev,
apt-utils), and docker or podman: the apk and pacman indexes are built in
Alpine and Arch containers (`createrepo_c` too, when it is not installed).

`scripts/packages-smoke.sh` installs a signed snapshot through the site in
Debian, Fedora, openSUSE, Alpine and Arch containers, plus the archive method,
and drives the agent service under SysV and OpenRC. Run it after changing
anything under `packaging/`, the installers or the publisher; its header shows
how to make a signed snapshot with a throwaway key.

## The package site

`scripts/publish-packages-pages.sh` builds the site from `dist/` (or a
release, `--from-release`) and with `--push` force-pushes it as a single commit
to the `gh-pages` branch of
[`orion-belt-dev/packages`](https://github.com/orion-belt-dev/packages), served
at `pkgs.orion-belt.dev` (a `CNAME` file in the site; DNS: `pkgs CNAME
orion-belt-dev.github.io`, not proxied). It holds the latest release only.

The `.deb` and `.rpm` packages sit at the site root (the apt pool and the RPM
repository, under the flat names install scripts use), the Alpine and Arch
repositories under `apk/<arch>` and `archlinux/<arch>`. GitHub Pages refuses
sites over 1 GB (the publisher fails above 950 MB, `ORION_MAX_SITE_MB`, warns
within 15% of it, and prints the size on every run), so the site hosts RPMs
for the architectures RPM distributions ship (x86_64, aarch64, ppc64le, s390x,
riscv64; `ORION_RPM_ARCHS`). The other RPMs, and every archive, are on the
GitHub release: `install.sh` and the console's agent script fall back to it.

## Release setup (maintainers)

Repository secrets on `orion-belt-dev/orion-belt`:

| Secret | Purpose |
|--------|---------|
| `GPG_PRIVATE_KEY` | packaging private key (armored or base64); signs checksums, packages, archives and the repository metadata |
| `GPG_PASSPHRASE` | its passphrase (empty for an unprotected key) |
| `APK_RSA_PRIVATE_KEY` | optional: `openssl genrsa 4096`; signs `.apk` files and the APKINDEX. Without it, `install.sh` installs verified release files on Alpine instead of using the repository |
| `PACKAGES_TOKEN` or `PACKAGES_DEPLOY_KEY` | write access to `orion-belt-dev/packages` (`GITHUB_TOKEN` cannot push to another repository) |

Without `GPG_PRIVATE_KEY` the release is unsigned; without a packages secret
the publish step fails rather than skipping.

```bash
git tag v1.4.0 && git push origin v1.4.0
```

Rebuild the site from an existing release:

```bash
ORION_GPG_KEY=<fpr> make publish-packages-pages FROM_RELEASE=1 PUSH=1
```

## Container images (GHCR)

Release tags push multi-arch images (amd64, arm64, arm/v7, arm/v6, 386,
ppc64le, s390x, riscv64). The Go build runs on the build machine and
cross-compiles, so only the final Alpine stage runs under emulation.

```bash
docker pull ghcr.io/orion-belt-dev/orion-belt-server:1.4.0
docker pull ghcr.io/orion-belt-dev/orion-belt-agent:1.4.0
# or :latest
```

Local build + push (after `docker login ghcr.io`):

```bash
DOCKER_TAG=1.4.0 make docker-push
```

## Production Docker Compose

Pull published images (always refresh `:latest`):

```bash
cp .env.prod.example .env.prod
make docker-prod-up
```

Agent hosts:

```bash
cp .env.prod.agent.example .env.prod.agent
make docker-prod-agent-up
```

Override tag with `ORION_IMAGE_TAG=1.4.0` to pin. Source builds stay on `docker-compose.server.yml`.

## First-run setup

See [SETUP.md](SETUP.md). After install:

```bash
sudoedit /etc/orion-belt/server.yaml
sudo /usr/lib/orion-belt/orion-belt-server-service setup
orion-belt-server setup
```

The server package runs as the `orionbelt` user, which it creates before
unpacking, and owns `/etc/orion-belt`, `/var/lib/orion-belt` and
`/var/log/orion-belt`. Install creates the SSH host key
`/etc/orion-belt/ssh_host_key` with `ssh-keygen`; where that is missing (for
example minimal openSUSE, which skips recommended packages) the server creates
the key on first start and logs its fingerprint.
