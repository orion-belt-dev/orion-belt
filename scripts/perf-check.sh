#!/usr/bin/env bash
# Performance regression gate for the gateway benchmarks.
#
# Runs the benchmark suite, reduces each metric to its best-of-N result, and
# compares that against the recorded baseline in perf-baseline.txt.
#
#   scripts/perf-check.sh            # fail if a metric regressed
#   scripts/perf-check.sh --update   # re-record the baseline
#
# Two tolerances, because the metrics differ in how noisy they are:
#
#   * Allocation counts (B/op, allocs/op) are deterministic for a given source
#     tree, so they gate tightly (PERF_ALLOC_TOLERANCE, default 5%). These
#     catch most real regressions — an extra copy or a new per-request
#     allocation shows up here long before it shows up in wall-clock time.
#
#   * Timing and throughput (ns/op, p50-ms, sessions/s, MB/s) gate loosely
#     (PERF_TIME_TOLERANCE, default 50%), because shared CI runners are noisy
#     enough that a tight timing gate would fail on unrelated PRs. Best-of-N
#     helps — noise only ever makes a benchmark slower, never faster — but it
#     does not eliminate the spread. Measured run-to-run spread on an idle
#     machine is up to ~19% for ns/op, so 50% leaves headroom for a busy runner
#     while still catching the kind of regression that matters.
#
#   * Tail latencies (p95-ms, p99-ms) are recorded but NOT gated: they are the
#     extreme of a bounded sample and swung 17-26% between back-to-back local
#     runs, which is too noisy to fail a build on. They are the most useful
#     numbers to eyeball as a trend across the tracked history, which is why
#     they stay in the baseline file.
#
# Machine-dependent by nature: a baseline recorded on a laptop will not match
# one from a CI runner. The committed baseline is the CI runner's; regenerate
# it there (the perf workflow's --update mode), not locally.
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT"

BASELINE="${PERF_BASELINE:-perf-baseline.txt}"
# Fixed iteration counts, not a time budget: a fixed count makes each run
# comparable to the last, and the concurrency sub-benchmarks need enough
# iterations that every simulated client gets real work (>= ~8 ops per client
# at the highest concurrency, which is 64).
BENCHTIME="${PERF_BENCHTIME:-1000x}"
COUNT="${PERF_COUNT:-5}"
PACKAGES="${PERF_PACKAGES:-./pkg/...}"
ALLOC_TOLERANCE="${PERF_ALLOC_TOLERANCE:-5}"
TIME_TOLERANCE="${PERF_TIME_TOLERANCE:-50}"
# Recorded for trend tracking but never gated — see the header note on tails.
UNGATED_METRICS="${PERF_UNGATED_METRICS:-p95-ms p99-ms}"
RAW_OUT="${PERF_RAW_OUTPUT:-}"

UPDATE=0
[[ "${1:-}" == "--update" ]] && UPDATE=1

CUR="$(mktemp)"
RAW="$(mktemp)"
trap 'rm -f "$CUR" "$RAW"' EXIT

echo "==> Running benchmarks (benchtime=$BENCHTIME count=$COUNT)"
# -run '^$' skips tests so only benchmarks execute. GOMAXPROCS is pinned so the
# concurrency sub-benchmarks schedule the same way run to run.
GOMAXPROCS="${PERF_GOMAXPROCS:-4}" \
  go test $PACKAGES -run '^$' -bench . -benchmem \
  -benchtime="$BENCHTIME" -count="$COUNT" | tee "$RAW"

if [[ -n "$RAW_OUT" ]]; then
  cp "$RAW" "$RAW_OUT"
  echo "==> Raw benchmark output saved to $RAW_OUT"
fi

# Go benchmark lines are: NAME-GOMAXPROCS  ITERS  (VALUE UNIT)...
#   BenchmarkX/sub-4   1000   1339452 ns/op   98747 B/op   683 allocs/op
# Reduce each (benchmark, metric) across -count runs to its best value: minimum
# for lower-is-better metrics, maximum for higher-is-better ones.
awk '
  function better(unit, new, old) {
    # Higher-is-better metrics; everything else is lower-is-better.
    if (unit == "sessions/s" || unit == "MB/s" || unit == "ops/s") return new > old
    return new < old
  }
  /^Benchmark/ {
    name = $1
    sub(/-[0-9]+$/, "", name)   # strip the trailing GOMAXPROCS suffix
    for (i = 3; i < NF; i += 2) {
      value = $i
      unit  = $(i + 1)
      if (value !~ /^[0-9.]+$/) continue
      key = name "\t" unit
      if (!(key in best) || better(unit, value + 0, best[key])) best[key] = value + 0
    }
  }
  END {
    for (key in best) printf "%s\t%s\n", key, best[key]
  }
' "$RAW" | sort > "$CUR"

if [[ ! -s "$CUR" ]]; then
  echo "error: no benchmark results parsed; did the benchmarks fail to build?" >&2
  exit 1
fi

if [[ $UPDATE -eq 1 ]]; then
  {
    echo "# Gateway performance baseline for scripts/perf-check.sh."
    echo "# Format: benchmark<TAB>metric<TAB>best-of-${COUNT} value (benchtime=${BENCHTIME})."
    echo "#"
    echo "# Recorded on the CI runner — numbers from a different machine will not"
    echo "# match. Regenerate with the 'Performance benchmarks' workflow"
    echo "# (workflow_dispatch, update_baseline=true), not from a laptop."
    cat "$CUR"
  } > "$BASELINE"
  echo "==> Baseline written to $BASELINE"
  sed 's/^/    /' "$CUR"
  exit 0
fi

if [[ ! -f "$BASELINE" ]]; then
  echo "error: $BASELINE missing; create it with scripts/perf-check.sh --update" >&2
  exit 1
fi

echo
echo "==> Comparing against $BASELINE (alloc ${ALLOC_TOLERANCE}%, timing ${TIME_TOLERANCE}%)"
fail=0
new=0

while IFS=$'\t' read -r bench metric cur; do
  if [[ " $UNGATED_METRICS " == *" $metric "* ]]; then
    continue
  fi

  base="$(awk -F'\t' -v b="$bench" -v m="$metric" '$1 == b && $2 == m { print $3 }' "$BASELINE")"
  if [[ -z "$base" ]]; then
    printf '    %-62s %-12s %12s  (new, not in baseline)\n' "$bench" "$metric" "$cur"
    new=1
    continue
  fi

  case "$metric" in
    B/op|allocs/op) tol="$ALLOC_TOLERANCE" ;;
    *)              tol="$TIME_TOLERANCE"  ;;
  esac

  # Percent change, signed so that positive always means "worse".
  read -r delta regressed < <(awk -v c="$cur" -v b="$base" -v m="$metric" -v t="$tol" 'BEGIN {
    higher_is_better = (m == "sessions/s" || m == "MB/s" || m == "ops/s")
    if (b == 0) { print 0, 0; exit }
    pct = (c - b) / b * 100
    if (higher_is_better) pct = -pct
    printf "%.1f %d\n", pct, (pct > t) ? 1 : 0
  }')

  if [[ "$regressed" == "1" ]]; then
    printf '  x %-62s %-12s %12s  REGRESSED %+.1f%% from %s\n' "$bench" "$metric" "$cur" "$delta" "$base"
    fail=1
  elif awk -v d="$delta" -v t="$tol" 'BEGIN { exit !(d < -t) }'; then
    printf '  + %-62s %-12s %12s  improved %+.1f%% from %s\n' "$bench" "$metric" "$cur" "$delta" "$base"
  fi
done < "$CUR"

if [[ $fail -eq 1 ]]; then
  cat >&2 <<'EOF'

Performance regressed beyond tolerance.

If the change is expected (e.g. added crypto work, a deliberate trade-off),
re-record the baseline via the "Performance benchmarks" workflow with
update_baseline=true and commit the result, noting why in the commit message.

If it is not expected, profile the affected benchmark:
    go test ./pkg/server/ -run '^$' -bench BenchmarkSessionEstablish -cpuprofile cpu.out
    go tool pprof -top cpu.out
EOF
  exit 1
fi

if [[ $new -eq 1 ]]; then
  echo
  echo "New benchmarks are not yet gated — record them with:"
  echo "    scripts/perf-check.sh --update"
fi

echo "==> No performance regression"
