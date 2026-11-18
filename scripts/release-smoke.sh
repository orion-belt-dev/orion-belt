#!/usr/bin/env bash
# Quick HTTP checks before a release. See docs/RELEASE_SMOKE.md.
#   ORION_API=http://127.0.0.1:8080 bash scripts/release-smoke.sh
# Optional: ORION_API_KEY=... or ORION_SESSION_TOKEN=...
set -euo pipefail

API="${ORION_API:-http://127.0.0.1:8080}"
API="${API%/}"
FAIL=0

auth_headers=()
if [[ -n "${ORION_API_KEY:-}" ]]; then
  auth_headers=(-H "X-API-Key: ${ORION_API_KEY}")
elif [[ -n "${ORION_SESSION_TOKEN:-}" ]]; then
  auth_headers=(-H "X-Session-Token: ${ORION_SESSION_TOKEN}")
fi

pass() { echo "  PASS  $*"; }
fail() { echo "  FAIL  $*" >&2; FAIL=1; }

echo "==> Release smoke against ${API}"

# --- unauthenticated gates ---
if curl -fsS "${API}/health" >/dev/null; then
  pass "GET /health"
elif curl -fsS "${API}/api/v1/version" >/dev/null; then
  pass "GET /api/v1/version"
else
  fail "health/version unreachable"
fi

if metrics=$(curl -fsS "${API}/metrics" 2>/dev/null); then
  if grep -q 'orion_belt_up' <<<"$metrics"; then
    pass "GET /metrics (orion_belt_up)"
  else
    fail "GET /metrics missing orion_belt_up"
  fi
else
  fail "GET /metrics"
fi

if oa=$(curl -fsS "${API}/api/v1/openapi.yaml" 2>/dev/null); then
  if grep -q '^openapi:' <<<"$oa"; then
    pass "GET /api/v1/openapi.yaml"
  else
    fail "openapi.yaml unexpected body"
  fi
else
  fail "GET /api/v1/openapi.yaml"
fi

# --- optional authenticated gates ---
if ((${#auth_headers[@]})); then
  echo "==> Authenticated checks"
  if curl -fsS "${auth_headers[@]}" "${API}/api/v1/sessions" >/dev/null; then
    pass "GET /api/v1/sessions"
  else
    fail "GET /api/v1/sessions"
  fi
  if curl -fsS "${auth_headers[@]}" "${API}/api/v1/notifications/prefs" >/dev/null; then
    pass "GET /api/v1/notifications/prefs"
  else
    fail "GET /api/v1/notifications/prefs"
  fi
  if curl -fsS "${auth_headers[@]}" "${API}/api/v1/admin/permissions" >/dev/null; then
    pass "GET /api/v1/admin/permissions"
  else
    echo "  SKIP  GET /api/v1/admin/permissions (need admin/operator key)"
  fi
else
  echo "==> Skipping auth checks (set ORION_API_KEY or ORION_SESSION_TOKEN)"
fi

echo
if [[ "$FAIL" -ne 0 ]]; then
  echo "Release smoke FAILED" >&2
  exit 1
fi
echo "Release smoke PASSED"
echo "If this is the v1.0.0 candidate, finish the manual bits in docs/RELEASE_SMOKE.md before tagging."
