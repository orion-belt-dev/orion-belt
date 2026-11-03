# Security exceptions

Findings left in place, with a review date. Scanner ignore files are not used.

## GO-2026-5932

- Package: `golang.org/x/crypto@v0.58.0`, package `golang.org/x/crypto/openpgp`
- Advisory: [GO-2026-5932](https://pkg.go.dev/vuln/GO-2026-5932)
- Why: `openpgp` is unmaintained and has no fixed release. This module is required for `golang.org/x/crypto/argon2` (`pkg/auth`). `govulncheck` on 2026-10-22 (Go 1.27.3) reports the vulnerable symbols are not called.
- Owner: mdarguechi
- Review by: 2027-01-20

## AVD-KSV-0050

- File: `deploy/kubernetes/orion-belt-agent.yaml`, ClusterRole `orion-belt-agent`
- Check: [AVD-KSV-0050](https://avd.aquasec.com/misconfig/ksv050) (manage RBAC: create/update/delete rolebindings, plus `impersonate` users)
- Why: issuing and removing RoleBindings and ClusterRoleBindings for approved k8s grants, and impersonating `orion:<user>` for kubectl, is what the in-cluster agent is for (#37, #38). Mitigations: the agent refuses any binding not named `orion-*` and any subject that is not an `orion:` user (`pkg/kube/client.go`), only lists objects labelled `app.kubernetes.io/managed-by=orion-belt`, and the manifest documents narrowing `bind` with `resourceNames` to the ClusterRoles grants may use.
- Owner: mdarguechi
- Review by: 2027-01-22

## AVD-DS-0002 (server image)

- File: `docker/Dockerfile`, stage `server`
- Check: [AVD-DS-0002](https://avd.aquasec.com/misconfig/ds002) (no non-root `USER`)
- Why: the image has no `USER` line so the entrypoint can start as root and hand the mounted volumes to `orionbelt` (uid 10001); volumes created by earlier images are root-owned, and a plain `USER` would break their upgrade. `docker/server-entrypoint.sh` then re-executes itself through `su-exec orionbelt`, so the server never runs as root. Verified 2026-11-03 with root-owned volumes, a mounted 0600 config and a templated config. Starting the container with `--user 10001` skips the root step entirely.
- Owner: mdarguechi
- Review by: 2027-02-01

## AVD-DS-0002 (agent images)

- Files: `docker/Dockerfile.agent`, `lab/compose/Dockerfile.agent`
- Check: [AVD-DS-0002](https://avd.aquasec.com/misconfig/ds002) (no non-root `USER`)
- Why: the agent starts each session as the requested local user (setuid/setgid drop), which needs root, as the packaged agent service does. It opens no listening port; it only dials out to the gateway, which it authenticates by host key or Host CA certificate.
- Owner: mdarguechi
- Review by: 2027-02-01

## gitleaks (history)

- Files: `.gitleaksignore` (45 exact fingerprints covering the 66 matches)
- Check: gitleaks `generic-api-key` and `private-key` over the git history
- Why: reviewed 2026-11-03 without printing values; none is a credential. 42 are base64 data blobs (64-symbol alphabet, 1-1.5 KB) inside the built docs bundles under `assets/js/`; 17 are the test placeholder `0123456789abcdef…` and similar in `*_test.go`; 3 are "private keys" in `pkg/api/agent_install_test.go` history that do not parse as keys; 2 are a fake Slack-style string in `pkg/plugin/manager_test.go` that does not match the token format; 2 more are dummy test values. The ignore file lists exact fingerprints, so any new finding still fails the scan.
- Owner: mdarguechi
- Review by: 2027-02-01
