---
slug: verifiable-releases
title: How to check that an Orion Belt binary is the one we built
description: From 1.5.0, every Orion Belt release file and container image has SLSA level 3 provenance and a GitHub attestation next to its GPG signature. What each proof tells you, how to check it, and the rest of the supply-chain work behind it.
date: 2026-11-18T12:00:00Z
authors: [orion]
tags: [security, ops]
keywords: [SLSA provenance, signed releases, gh attestation verify, slsa-verifier, OpenSSF Scorecard, supply chain security PAM]
---

An access gateway holds the keys to every machine behind it, so the binary
you run matters as much as the code you read. From **1.5.0**, each Orion
Belt release lets you check not only who signed a file, but how and from
what it was built.

{/* truncate */}

## Three proofs, three questions

| Proof | Question it answers |
| --- | --- |
| GPG signature (`<file>.sig`, `checksums.txt.sig`) | Was this file signed with the Orion Belt release key? |
| SLSA provenance (`orion-belt-v1.5.0.intoto.jsonl`) | Was it built by our release workflow, from the `v1.5.0` tag of our repository? |
| GitHub attestation | The same, recorded by GitHub, for release files and both container images |

The GPG key signs every release file and the package repositories. It
proves a file came from someone with the key. It can't tell you what was
built: a signed binary built from a modified tree would pass.

Provenance closes that gap. The release workflow records which repository,
commit and workflow produced each file, and that statement is signed with a
short-lived certificate issued to the workflow run itself. No maintainer
holds a key that could sign a false one.

## Checking a file

```bash
TAG=v1.5.0
FILE=orion-belt_1.5.0_linux_amd64.tar.gz
gh release download "$TAG" -R orion-belt-dev/orion-belt \
  -p "$FILE" -p "orion-belt-$TAG.intoto.jsonl"

slsa-verifier verify-artifact "$FILE" \
  --provenance-path "orion-belt-$TAG.intoto.jsonl" \
  --source-uri github.com/orion-belt-dev/orion-belt \
  --source-tag "$TAG"

gh attestation verify "$FILE" --repo orion-belt-dev/orion-belt
```

And an image:

```bash
gh attestation verify oci://ghcr.io/orion-belt-dev/orion-belt-server:1.5.0 \
  --repo orion-belt-dev/orion-belt
```

The images also carry BuildKit provenance and an SBOM. The full walkthrough,
including GPG, is in [Verify a release](/docs/quickstart/verify-release).

If you install from [pkgs.orion-belt.dev](https://pkgs.orion-belt.dev), your
package manager already checks the GPG signature on every package.

## Why only from 1.5.0

Provenance has to be produced at build time. We could generate statements for
1.4.0 and earlier now, but they would describe builds that never happened that
way. Older releases keep their GPG signatures and nothing more.

## The rest of the work

Provenance was the visible part of a wider pass over how the project is built
and maintained:

- Every GitHub Action is pinned to a commit SHA (except the SLSA generator,
  which has to be referenced by its release tag for `slsa-verifier` to trust
  it). Container base images in the lab are pinned by digest, and workflow
  tokens get write access only in the jobs that need it.
- `master` needs two approving reviews, a fresh approval after the last push,
  and nine passing CI checks, including a gate that fails on any known
  vulnerability in a dependency.
- Go fuzz targets cover scope parsing, SCP file names, shell word splitting
  and password hash verification. The last one found a panic on malformed
  stored hashes, fixed in 1.5.0.
- A [security policy](https://github.com/orion-belt-dev/orion-belt/blob/master/SECURITY.md)
  with private reporting through GitHub advisories or
  maintainers@orion-belt.dev.

The project's
[OpenSSF Scorecard](https://scorecard.dev/viewer/?uri=github.com/orion-belt-dev/orion-belt)
went from 7.1 to 9.4, and it holds the
[OpenSSF Best Practices](https://www.bestpractices.dev/projects/16726) passing
badge. Scores aren't security, but they are a public checklist anyone can
re-run against us.

## Related

- [Orion Belt v1.5](/blog/orion-belt-v1-5)
- [Linux packages and labs](/blog/linux-packages-and-labs)
