#!/usr/bin/env bash
# Per-package coverage ratchet.
#
# Coverage is being raised toward ~80% incrementally, one package per PR. This
# gate does not demand 80% everywhere today — it only forbids sliding backwards
# from whatever each package has already earned, recorded in coverage-baseline.txt.
#
#   scripts/coverage-check.sh            # verify no package regressed
#   scripts/coverage-check.sh --update   # re-record the baseline after adding tests
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT"

BASELINE="coverage-baseline.txt"
PROFILE="${COVERAGE_PROFILE:-coverage.out}"
# Coverage percentages are deterministic for a given source tree, but allow a
# hair of slack so a statement-count rounding shift can't fail an unrelated PR.
TOLERANCE="${COVERAGE_TOLERANCE:-0.5}"

UPDATE=0
[[ "${1:-}" == "--update" ]] && UPDATE=1

CUR="$(mktemp)"
RAW="$(mktemp)"
trap 'rm -f "$CUR" "$RAW"' EXIT

echo "==> Running tests with coverage"
go test ./pkg/... ./plugins/... -coverprofile="$PROFILE" -covermode=atomic | tee "$RAW"

# `go test -cover` prints one authoritative line per package. Shapes:
#   ok  \tPKG\t0.66s\tcoverage: 3.8% of statements     -> has tests
#       \tPKG\t\tcoverage: 0.0% of statements          -> compiles, no tests
#   ok  \tPKG\t0.30s\tcoverage: [no statements]        -> nothing to measure, skip
#   ?   \tPKG\t[no test files]                         -> skip
awk '{
  pct = ""
  for (i = 1; i <= NF; i++) {
    if ($i == "coverage:") { pct = $(i + 1); break }
  }
  sub(/%$/, "", pct)
  if (pct !~ /^[0-9.]+$/) next
  pkg = ($1 == "ok") ? $2 : $1
  # Dynamic (string) regex: BSD awk mis-parses "/" inside a bracket
  # expression in a /.../ literal.
  sub("^github\\.com/[^/]+/[^/]+/", "", pkg)
  printf "%s\t%s\n", pkg, pct
}' "$RAW" | sort > "$CUR"

if [[ ! -s "$CUR" ]]; then
  echo "error: no coverage data produced" >&2
  exit 1
fi

if [[ $UPDATE -eq 1 ]]; then
  {
    echo "# Per-package statement coverage baseline for the coverage ratchet."
    echo "# Regenerate with: scripts/coverage-check.sh --update"
    echo "# A package may never drop below its recorded value; raising it is the goal (~80%)."
    cat "$CUR"
  } > "$BASELINE"
  echo "==> Baseline written to $BASELINE"
  sed 's/^/    /' "$CUR"
  exit 0
fi

if [[ ! -f "$BASELINE" ]]; then
  echo "error: $BASELINE missing; create it with scripts/coverage-check.sh --update" >&2
  exit 1
fi

echo
echo "==> Comparing against $BASELINE (tolerance ${TOLERANCE}pp)"
fail=0
gained=0

while IFS=$'\t' read -r pkg cur; do
  base="$(awk -F'\t' -v p="$pkg" '$1 == p { print $2 }' "$BASELINE")"
  if [[ -z "$base" ]]; then
    printf '    %-50s %6s%%  (new package, not yet in baseline)\n' "$pkg" "$cur"
    gained=1
    continue
  fi
  if awk -v c="$cur" -v b="$base" -v t="$TOLERANCE" 'BEGIN { exit !(c < b - t) }'; then
    printf '  x %-50s %6s%%  REGRESSED from %s%%\n' "$pkg" "$cur" "$base"
    fail=1
  elif awk -v c="$cur" -v b="$base" -v t="$TOLERANCE" 'BEGIN { exit !(c > b + t) }'; then
    printf '  + %-50s %6s%%  up from %s%%\n' "$pkg" "$cur" "$base"
    gained=1
  fi
done < "$CUR"

if [[ $fail -eq 1 ]]; then
  echo
  echo "Coverage regressed. Add tests for the affected package, or if the drop is"
  echo "intentional (e.g. covered code was deleted), re-record with:"
  echo "    scripts/coverage-check.sh --update"
  exit 1
fi

if [[ $gained -eq 1 ]]; then
  echo
  echo "Coverage improved — commit the new baseline:"
  echo "    scripts/coverage-check.sh --update"
else
  echo "    (no change)"
fi

echo "==> No package regressed"
