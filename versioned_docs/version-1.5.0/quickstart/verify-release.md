---
sidebar_position: 6
title: Verify a release
description: Check Orion Belt release files and container images with GPG signatures, SLSA provenance and GitHub attestations.
---

# Verify a release

The package repositories check signatures for you: apt, dnf, zypper, apk and
pacman refuse a package that is not signed by the release key. This page is for
files you download by hand from a
[GitHub release](https://github.com/orion-belt-dev/orion-belt/releases), and
for the container images.

:::info[New in 1.5.0]
SLSA provenance and GitHub attestations start with 1.5.0. Older releases
have GPG signatures only.
:::

Every release has three independent proofs. Any one of them is enough to
trust a file; they answer different questions.

| Proof | Answers | Tool |
| --- | --- | --- |
| GPG signature | Was it signed by the release key? | `gpg` |
| SLSA provenance | Was it built by the release workflow, from this tag? | `slsa-verifier` |
| GitHub attestation | Same, for files and images, recorded in GitHub | `gh` |

## GPG signature

The release key is `orion-belt-release-key.asc`, with fingerprint
`F1E4 7F7B 036D 5202 262E 174F 613B 2217 A895 8DA9`. It also signs the package
repositories; compare it with the one on
[pkgs.orion-belt.dev](https://pkgs.orion-belt.dev).

```bash
TAG=v1.5.0
FILE=orion-belt_1.5.0_linux_amd64.tar.gz
gh release download "$TAG" -R orion-belt-dev/orion-belt \
  -p "$FILE" -p checksums.txt -p checksums.txt.sig -p orion-belt-release-key.asc

gpg --show-keys --with-fingerprint orion-belt-release-key.asc
gpg --import orion-belt-release-key.asc
gpg --verify checksums.txt.sig checksums.txt
sha256sum --ignore-missing -c checksums.txt
```

Every package and archive also has its own detached signature, `<file>.sig`.
`.deb` files carry an embedded signature and `.rpm` files a native one
(`rpm -K`).

## SLSA provenance

`orion-belt-<tag>.intoto.jsonl` is
[SLSA level 3](https://slsa.dev/spec/v1.0/levels) provenance for every file
in `checksums.txt`. It is signed by the SLSA generator running in GitHub
Actions, not by a key the maintainers hold. Check a file with
[slsa-verifier](https://github.com/slsa-framework/slsa-verifier):

```bash
gh release download "$TAG" -R orion-belt-dev/orion-belt -p "orion-belt-$TAG.intoto.jsonl"
slsa-verifier verify-artifact "$FILE" \
  --provenance-path "orion-belt-$TAG.intoto.jsonl" \
  --source-uri github.com/orion-belt-dev/orion-belt \
  --source-tag "$TAG"
```

## GitHub attestations

Release files and both container images have build attestations:

```bash
gh attestation verify "$FILE" --repo orion-belt-dev/orion-belt

gh attestation verify oci://ghcr.io/orion-belt-dev/orion-belt-server:1.5.0 \
  --repo orion-belt-dev/orion-belt
gh attestation verify oci://ghcr.io/orion-belt-dev/orion-belt-agent:1.5.0 \
  --repo orion-belt-dev/orion-belt
```

The images also carry BuildKit provenance and an SBOM:

```bash
docker buildx imagetools inspect ghcr.io/orion-belt-dev/orion-belt-server:1.5.0 \
  --format '{{json .Provenance}}'
docker buildx imagetools inspect ghcr.io/orion-belt-dev/orion-belt-server:1.5.0 \
  --format '{{json .SBOM}}'
```

## Check the version

A verified binary reports the release and the commit it was built from:

```bash
$ orion-belt-server --version
orion-belt-server version 1.5.0+78dd1087
```
