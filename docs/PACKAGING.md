# Packaging & installation

Orion Belt ships native packages for major Linux distributions via **GoReleaser + nFPM**.

## Packages

| Package | Contents | Formats |
|---------|----------|---------|
| `orion-belt` | gateway server + systemd unit + `/etc/orion-belt/server.yaml` | deb, rpm, apk |
| `orion-belt-agent` | reverse SSH agent + systemd unit | deb, rpm, apk |
| `orion-belt-tools` | `osh`, `ocp`, `oadmin` | deb, rpm, apk |

## Build locally

```bash
# Requires Go 1.26.5+ (see go.mod). Optional: nfpm or goreleaser.
make packages
# → dist/*.deb  dist/*.rpm  dist/*.apk  + raw binaries
```

Or:

```bash
./scripts/package.sh
```

## Install

### Debian / Ubuntu

```bash
sudo apt install ./orion-belt_*_amd64.deb
sudo apt install ./orion-belt-agent_*_amd64.deb
sudo apt install ./orion-belt-tools_*_amd64.deb
```

### RHEL / Rocky / Fedora / openSUSE

```bash
sudo rpm -Uvh orion-belt-*.rpm orion-belt-agent-*.rpm orion-belt-tools-*.rpm
# openSUSE:
sudo zypper install ./orion-belt-*.rpm
```

### Alpine

```bash
# Trusted (signed index + key in /etc/apk/keys):
sudo apk add orion-belt orion-belt-agent orion-belt-tools
# Local unsigned package (dev only):
sudo apk add --allow-untrusted ./orion-belt-*.apk
```

## Enable services

```bash
# Edit DB connection / JWT secret first
sudoedit /etc/orion-belt/server.yaml
sudo systemctl enable --now orion-belt-server

# On each target host
sudoedit /etc/orion-belt/agent.yaml
sudo systemctl enable --now orion-belt-agent
```

## Release (GitHub)

Push a tag `v*`. The Release workflow runs the CVE gate, then GoReleaser publishes archives + packages to GitHub Releases.

```bash
git tag v0.6.0
git push origin v0.6.0
```

## APT / YUM / APK repositories (signed)

Build packages, generate a packaging key, then publish **signed** static repo trees:

```bash
make packages
./scripts/gen-packaging-key.sh          # once per maintainer / org
export ORION_GPG_KEY="$(cat packaging/keys/orion-belt.fingerprint)"
ORION_REQUIRE_SIGN=1 make repos
# → repos/apt  repos/rpm  repos/apk  repos/keys  + SHA256SUMS(.asc)
```

Also:

```bash
make sign-artifacts   # dist/SHA256SUMS + .asc per artifact
```

Serve `repos/` over HTTPS (nginx, CDN, GitHub Pages, S3). Client install snippets:

| Format | Snippet |
|--------|---------|
| APT | `packaging/repos/apt.sources.example` (`signed-by=` keyring) |
| RPM/DNF/Zypper | `packaging/repos/rpm.repo.example` (`gpgcheck=1`) |
| Alpine | `packaging/repos/apk.repositories.example` |

Public key files published with the repo:

- `repos/keys/orion-belt.asc` / `.gpg`
- `repos/apt/orion-belt.gpg` (APT `signed-by`)
- `repos/rpm/orion-belt.asc` (RPM `gpgkey=`)

Verify:

```bash
gpg --import packaging/keys/orion-belt.asc
gpg --verify repos/SHA256SUMS.asc repos/SHA256SUMS
gpg --verify dist/SHA256SUMS.asc dist/SHA256SUMS
(cd dist && sha256sum -c SHA256SUMS)
```

### CI / GitHub Releases

Tag `v*` runs GoReleaser with GPG signing when these secrets are set:

| Secret | Purpose |
|--------|---------|
| `GPG_PRIVATE_KEY` | ASCII-armored packaging private key |
| `GPG_PASSPHRASE` | Passphrase (empty OK for unprotected keys) |

GoReleaser signs `checksums.txt` and Linux packages (`.asc`). The workflow also builds a signed `repos/` artifact for CDN upload.

For Alpine index signing, set `ORION_APK_PRIVKEY` / `ORION_APK_PUBKEY` when calling `publish-repos.sh`.

Wire the script into release CD after GoReleaser to refresh the hosted repo.

The Release workflow also builds a flat **Add-agent** mirror under `packages-site/` (artifact `orion-belt-packages-site`). To auto-push it to [`orion-belt-dev/packages`](https://github.com/orion-belt-dev/packages) `gh-pages`, set repo secret `PACKAGES_DEPLOY_KEY` (deploy key with write access). Without it, download the artifact and run `make publish-packages-pages PUSH=1` locally.

### Arch Linux

Binary packages via `packaging/arch/PKGBUILD` (reads GitHub release tarballs):

```bash
cd packaging/arch
# bump pkgver to match a released tag (without leading v)
makepkg -si
```

That installs `orion-belt`, `orion-belt-agent`, and `orion-belt-tools`. Publishing to the AUR is a follow-up once a stable version is tagged.

## First-run setup

See [SETUP.md](SETUP.md). After install:

```bash
sudoedit /etc/orion-belt/server.yaml
sudo systemctl enable --now orion-belt-server
orion-belt-server setup
```

To enroll hosts quickly, use the UI **Add agent** flow (or `POST /api/v1/admin/agents/install-script`). The default **package base URL** is the public GitHub Pages mirror:

```text
https://orion-belt-dev.github.io/packages
```

That site is the [`orion-belt-dev/packages`](https://github.com/orion-belt-dev/packages) repo (`gh-pages`). Refresh it after a release:

```bash
make publish-packages-pages FROM_RELEASE=1 PUSH=1
# or from local dist/:
make packages && make publish-packages-pages PUSH=1
```

For a **local** package server (dev builds, air-gapped, or custom `dist/`):

```bash
make packages
make serve-packages   # http://127.0.0.1:8765 — set that as Package base URL
```