# What v1.0 covers

v1.0.0 (2026-07-14) marks the SSH bastion and PAM feature set as stable:
the SSH certificate authority, MFA, just-in-time access, ReBAC, session
recording, live watch and the Linux packages. From v1.0 on, `/api/v1` follows
semver as described in [API_STABILITY.md](API_STABILITY.md).

OIDC, HA, RDP/VNC, the SDKs and the compliance reports were out of scope for
1.0. RDP/VNC, the SDKs and the compliance reports shipped in later minor
releases; see [ROADMAP.md](ROADMAP.md).

## Operating assumptions

1.0 assumes one gateway with PostgreSQL backups, not a cluster.

Back up the database:

```bash
pg_dump "$DATABASE_URL" -Fc -f orion-belt-$(date +%Y%m%d).dump
# restore:
# pg_restore --clean --if-exists -d "$RESTORE_URL" orion-belt-YYYYMMDD.dump
```

Back up `recording.storage_path` as well if you keep recordings for audits.

The operational docs that ship with 1.0:

| Doc | Covers |
|-----|--------|
| [SETUP.md](SETUP.md) | Installing and configuring a gateway |
| [DEPLOYMENT_HARDENING.md](DEPLOYMENT_HARDENING.md) | Settings to change before production |
| [OBSERVABILITY.md](OBSERVABILITY.md) | Logs, `/metrics` and example alerts |

## Release checks

Before tagging, maintainers run the smoke checks in
[RELEASE_SMOKE.md](RELEASE_SMOKE.md) (`make release-smoke`) against a gateway
built from the release candidate. They are not part of CI. The multi-distro QEMU lab
in [E2E_TEST_PLAN.md](E2E_TEST_PLAN.md) covers package installs and agent
connectivity in more depth.
