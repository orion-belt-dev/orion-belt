# Contributing to Orion-Belt

First of all, thank you for considering contributing to **Orion-Belt** ❤️
Community involvement is what makes open-source projects thrive.

Orion-Belt is an infrastructure and security-focused project, so we value
**clarity, correctness, security, and thoughtful design** in all contributions.

---

## Ways to Contribute

There are many ways to contribute, including:

* Reporting bugs
* Suggesting features or improvements
* Improving documentation
* Adding tests
* Implementing new features
* Reviewing pull requests
* Building plugins or integrations

No contribution is too small.

---

## Reporting Bugs

If you find a bug, please open an issue and include:

* A clear and descriptive title
* Steps to reproduce the issue
* Expected behavior
* Actual behavior
* Environment details (OS, Go version, config, etc.)
* Relevant logs or error messages

If the issue is security-sensitive, **do not open a public issue**.
Please see the [Security](#security) section below.

---

## Feature Requests

Feature requests are welcome.

When proposing a feature, please include:

* The problem you are trying to solve
* Why existing functionality is insufficient
* A suggested approach or design (if possible)

Well-thought-out proposals have a much higher chance of being accepted.

---

## Development Setup

### Requirements

* Go **1.26.5+** (pinned in `go.mod` / `toolchain`)
* PostgreSQL (or another supported database)
* Make
* SSH client (for testing)
* Optional: Docker (compose lab), QEMU + cloud-image-utils (QEMU lab), `nfpm` (local packages)

### Clone the Repository

```bash
git clone https://github.com/zrougamed/orion-belt.git
cd orion-belt
```

### Build

```bash
make build
```

### Run Tests

```bash
make test
make cve                    # 0-CVE gate (govulncheck)
ORION_CVE_E2E=1 go test ./e2e/cve/ -v
```

Coverage and microbenchmarks (when touching hot paths such as recording):

```bash
go test ./pkg/... -coverprofile=/tmp/ob.cov
go tool cover -func=/tmp/ob.cov | tail
go test ./pkg/recording/ -bench=. -benchtime=200ms
```

Target is higher package coverage over time; new packages should bring tests. Lab depth: [E2E_TEST_PLAN.md](E2E_TEST_PLAN.md). Before a release: [RELEASE_SMOKE.md](RELEASE_SMOKE.md) / `make release-smoke` ([V1_RELEASE_CRITERIA.md](V1_RELEASE_CRITERIA.md)).

### Coverage ratchet

We are raising unit coverage toward ~80%, one package per PR. `coverage-baseline.txt`
records what each package currently achieves, and CI fails if any package drops
below its recorded value — it does **not** require 80% everywhere today.

```bash
make coverage-check         # what CI runs: fail on any per-package regression
make coverage-baseline      # re-record after adding tests, then commit the diff
```

Raising one package is a good first contribution — pick one with a low number in
`coverage-baseline.txt` (`pkg/database`, `pkg/agent`, and `pkg/client` are the
largest gaps) and work through it. Two things we ask for:

* **Cover branches, not lines.** Exercise error paths, boundaries, and the
  behaviour a comment claims — not just the happy path taken for the percentage.
* **Don't pad.** Error returns that are unreachable by construction (for example
  `aes.NewCipher` on an already-validated 32-byte key) are fine to leave
  uncovered. A package sitting below 100% for that reason is expected.

### Performance benchmarks

Session establishment and gateway throughput are benchmarked, gated against
`perf-baseline.txt`, and run nightly. Every PR runs one iteration of each
benchmark to catch breakage, but timing is only gated nightly — a busy shared
runner can't fail your PR.

```bash
make bench          # run once, print results
make perf-check     # what the nightly job runs: fail on regression
```

Worth running locally before merging anything on the auth or proxy path. Full
details — what's measured, the tolerances, how to re-record the baseline — in
[BENCHMARKS.md](BENCHMARKS.md).

### Packages & labs

```bash
make packages               # deb/rpm/apk → dist/
make lab-compose-up         # multi-distro Docker agents
make lab-qemu-start         # full QEMU E2E bring-up (cleans by default)
```

See [lab/README.md](../lab/README.md), [PACKAGING.md](PACKAGING.md), and the QA plan [E2E_TEST_PLAN.md](E2E_TEST_PLAN.md).

---

## Coding Guidelines

Please follow these guidelines to keep the codebase consistent and maintainable:

* Use `gofmt` and `go vet`
* Prefer clarity over cleverness
* Write small, focused commits
* Add tests for new functionality
* Avoid breaking public APIs without discussion
* Keep security implications in mind at all times

---

## Commit Messages

Use clear and descriptive commit messages.
A good format is:

```
component: short description

Optional longer explanation if needed
```

Example:

```
agent: add heartbeat retry mechanism
```

---

## Pull Request Process

1. Fork the repository
2. Create a feature or fix branch
3. Make your changes
4. Ensure tests pass
5. Open a pull request against `main`
6. Clearly describe **what** and **why**

Pull requests may be reviewed for:

* Code quality
* Security implications
* Architectural consistency
* Documentation impact

When changing the HTTP API, update **`docs/openapi/openapi.yaml`** (served at `/api/v1/openapi.yaml`).  
When changing `/ui` behavior, update **`docs/SRS-UI.md`** so the SRS stays the acceptance baseline.

Please be patient — reviews may take time.

---

## Security

If you discover a security vulnerability, **do not disclose it publicly**.

Please report it privately by emailing:

**[med@zrouga.email](mailto:med@zrouga.email)**

Responsible disclosure is greatly appreciated.

---

## License

By contributing to Orion-Belt, you agree that your contributions will be licensed under the project's license, currently **Apache License 2.0 with the Commons Clause** (see [LICENSE](../LICENSE)).

---

## Community & Participation

Orion-Belt is built **with the community, for the community**.

Whether you are:

* A security engineer
* A DevOps practitioner
* A Go developer
* Or simply curious about SSH internals and access control

**You are welcome here.**

Feel free to:

* Ask questions
* Share ideas
* Challenge assumptions
* Propose improvements

Let’s build a **secure, auditable, and open access platform** together

Thank you for being part of the Orion-Belt community.
