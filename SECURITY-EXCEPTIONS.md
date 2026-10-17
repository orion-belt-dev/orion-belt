# Security exceptions

Findings left in place, with a review date. Scanner ignore files are not used.

## GO-2026-5932

- Package: `golang.org/x/crypto@v0.57.0`, package `golang.org/x/crypto/openpgp`
- Advisory: [GO-2026-5932](https://pkg.go.dev/vuln/GO-2026-5932)
- Why: `openpgp` is unmaintained and has no fixed release. This module is required for `golang.org/x/crypto/argon2` (`pkg/auth`). `govulncheck` on 2026-10-17 (Go 1.27.2) reports the vulnerable symbols are not called.
