# Security policy

Orion-Belt is an access gateway, so we treat security reports as our top
priority.

## Supported versions

Security fixes go into the latest minor release (currently 1.4.x) and are
shipped as a new patch release. Older releases do not get backports; upgrade
to the latest release to receive fixes.

## Reporting a vulnerability

Do not open a public issue, pull request or discussion for a security problem.

Report it privately through either channel:

- GitHub: [report a vulnerability](https://github.com/orion-belt-dev/orion-belt/security/advisories/new)
  (preferred; keeps the discussion and the fix in one private advisory).
- Email: [maintainers@orion-belt.dev](mailto:maintainers@orion-belt.dev).

Please include:

- the affected version or commit, and how Orion-Belt is deployed
  (packages, container images, Kubernetes);
- the component involved (server, gateway, agent, web UI, CLI, SDK);
- steps to reproduce or a proof of concept, and the impact you observed.

## What to expect

- We acknowledge your report within 3 working days.
- We confirm or rule out the issue, and share our assessment, within 10
  working days.
- Once a fix is released we publish a GitHub security advisory and credit
  you, unless you prefer to stay anonymous.

Please give us a reasonable chance to release a fix before disclosing
anything publicly. We aim to ship fixes for confirmed issues within 90 days.

## Scope

In scope: the code in this repository and the release artifacts built from
it (binaries, packages, container images, install scripts, SDKs).

Out of scope: vulnerabilities in third-party dependencies that are not
reachable from Orion-Belt (report those upstream), and issues that need an
already compromised administrator account or host.
