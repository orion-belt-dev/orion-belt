# Release smoke

Lightweight checks to run against a release candidate before tagging. Deeper multi-distro coverage still lives in [E2E_TEST_PLAN.md](E2E_TEST_PLAN.md).

## Automated

Running gateway required (`ORION_API` defaults to `http://127.0.0.1:8080`):

```bash
make release-smoke
# or
ORION_API=http://127.0.0.1:8080 bash scripts/release-smoke.sh
```

Looks for:

- `GET /health` (or `/api/v1/version`)
- `GET /metrics` containing `orion_belt_up`
- `GET /api/v1/openapi.yaml` looking like OpenAPI

If you pass `ORION_API_KEY` or `ORION_SESSION_TOKEN`, it also hits `/sessions`, notification prefs, and (when allowed) `/admin/permissions`.

## Worth doing by hand

### Auth
- [ ] log in with a key (or your usual path) and call something protected
- [ ] local admin still works

### Access
- [ ] list / grant / tweak / revoke a permission
- [ ] file a JIT request (try `access_type`), approve or reject

### Sessions
- [ ] start a session, confirm a recording shows up
- [ ] play it back (gzip/OBGZ1 or plain)
- [ ] **Watch** an active session, then stop

### Binary / ops
- [ ] `--version` / `/api/v1/version` match what you think you built
- [ ] metrics + JSON logs look sane

## With the QEMU lab

```bash
make lab-qemu-start
make release-smoke
# then poke /ui and OpenSSH as above
```

Jot who signed off and which version if you’re filing a release:

| Who | When | Version |
|-----|------|---------|
| | | |
