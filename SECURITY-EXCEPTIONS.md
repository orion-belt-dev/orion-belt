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
