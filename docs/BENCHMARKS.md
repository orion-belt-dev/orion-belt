# Performance benchmarks

A repeatable benchmark suite for the two things that make a gateway feel slow:
how long it takes to establish a session, and how fast it moves bytes once one
is established. It runs nightly in CI so regressions surface in a build rather
than in someone's terminal.

- Benchmarks: [`pkg/server/bench_test.go`](../pkg/server/bench_test.go)
- Gate: [`scripts/perf-check.sh`](../scripts/perf-check.sh)
- Baseline: [`perf-baseline.txt`](../perf-baseline.txt)
- Workflow: [`.github/workflows/perf.yml`](../.github/workflows/perf.yml)

## Running them

```bash
make bench          # run once, print results
make perf-check     # run and fail if anything regressed vs the baseline
make perf-baseline  # re-record the baseline (see "Updating the baseline")
```

To dig into one benchmark:

```bash
go test ./pkg/server/ -run '^$' -bench BenchmarkSessionEstablish -benchmem -benchtime=1000x
```

## What is measured

Everything runs against the in-memory `fakeStore` over loopback TCP. There is
no Postgres and no real network in the numbers, which is the point: a movement
here means our code changed, not that the database or the runner's network did.

### Control plane — session establishment

`BenchmarkSessionEstablish` measures one full session establishment, serially:
TCP connect, SSH handshake, and public-key/certificate authentication —
everything between "user runs `ssh`" and "the connection is authenticated". It
covers all three credential types the gateway accepts, because they do
materially different work:

| Sub-benchmark   | Path                                                        |
| --------------- | ----------------------------------------------------------- |
| `legacy_pubkey` | Static authorized-key lookup — the pre-SSH-CA path           |
| `user_cert`     | User-CA certificate validation — the SSH CA client path      |
| `host_cert`     | Host-CA certificate validation — the reverse-dial agent path |

`ns/op` is end-to-end latency for one session. `allocs/op` and `B/op` matter
just as much: they are deterministic, so they catch a regression long before it
is visible in wall-clock time on a noisy runner.

### Control plane — establishment under concurrent load

`BenchmarkSessionEstablishUnderLoad` runs the same establishment with a fixed
number of clients connecting at once (8, 32, 64), and reports:

- `sessions/s` — authenticated sessions completed per second, the headline
  throughput number
- `p50-ms`, `p95-ms`, `p99-ms` — the latency spread across those sessions

Concurrency is a fixed worker count rather than `b.RunParallel`'s
GOMAXPROCS-scaled parallelism, so `clients_32` means 32 clients on a 4-core
runner and on a 16-core laptop alike. Without that, baselines would not be
comparable across machines.

### Data plane — proxied throughput

`BenchmarkGatewayProxyThroughput` measures agent→client bytes per second
through `proxyConnection`, with and without session recording:

- `proxy_only` — the bare copy loop
- `recorded` — the same, with a `SessionRecorder` in the path

The `MB/s` figure is **not** an achievable wire throughput: reads come from
memory and writes go to `io.Discard`, so the SSH transport's own encryption and
windowing are deliberately excluded. It is an upper bound that isolates the
gateway's per-byte overhead, and the gap between the two variants is what the
recording layer costs every byte a user sees. On current code, recording is
the dominant data-plane cost by a wide margin — worth remembering before
attributing a slow session to the network.

## The regression gate

`scripts/perf-check.sh` runs the suite, reduces each metric to its **best of N**
runs, and compares against `perf-baseline.txt`. Best-of-N rather than the mean
because noise only ever makes a benchmark slower, never faster, so the minimum
is the closest thing to a clean measurement a shared runner can give.

Metrics gate at two different tolerances, because they are not equally noisy:

| Metric group                              | Tolerance | Why                                                                                                     |
| ----------------------------------------- | --------- | ------------------------------------------------------------------------------------------------------- |
| `B/op`, `allocs/op`                       | 5%        | Deterministic for a given source tree. This is where subtle regressions actually get caught.              |
| `ns/op`, `p50-ms`, `sessions/s`, `MB/s`   | 50%       | Measured run-to-run spread on an idle machine reaches ~19%; 50% leaves headroom for a busy shared runner. |
| `p95-ms`, `p99-ms`                        | not gated | Tails of a bounded sample; swung 17–26% between back-to-back local runs. Recorded for trends only.        |

Tunable via `PERF_ALLOC_TOLERANCE`, `PERF_TIME_TOLERANCE`, `PERF_UNGATED_METRICS`,
`PERF_BENCHTIME`, `PERF_COUNT`, `PERF_PACKAGES`, `PERF_BASELINE`.

## When it runs

- **Every PR** ([`ci.yml`](../.github/workflows/ci.yml)) — one iteration of every
  benchmark (`-benchtime=1x`). Far too few to time anything; enough to catch a
  benchmark that no longer builds or that panics. No timing gate, so a busy
  runner cannot fail an unrelated PR.
- **Nightly at 03:17 UTC** ([`perf.yml`](../.github/workflows/perf.yml)) — the full
  suite with the regression gate.
- **On demand** — `workflow_dispatch` on `perf.yml`, to check a branch before
  merging something performance-sensitive.

## Results over time

Each nightly run uploads `bench-raw.txt` and the baseline as a
`benchmark-results-<run>` artifact (90-day retention) and prints the benchmark
lines to the run summary, so any run's numbers are readable without downloading
anything. The long-term record is `perf-baseline.txt`'s git history — each
commit to it marks a point where performance moved and says why.

## Updating the baseline

The committed baseline is **seeded with allocation metrics only**. `B/op` and
`allocs/op` are properties of the source tree rather than the machine (they
drift <0.02% run to run), so they gate correctly anywhere. Timing metrics are
machine-specific, and seeding them from a laptop would have failed on the first
CI run — so they are absent, reported as "new, not in baseline", and pass.

To bring the timing gate live, record it on the runner:

1. Run the **Performance benchmarks** workflow with `update_baseline=true`.
2. Download the `benchmark-results-<run>` artifact.
3. Commit its `perf-baseline.txt`, saying in the commit message why the numbers
   moved.

The workflow holds no write permission and will not commit for you — a
regression should never be able to quietly re-baseline itself away.

The same steps apply later whenever a change legitimately moves the numbers.
Baselines recorded on a different machine than the runner will not compare
meaningfully, so avoid committing a locally generated one.

## Adding a benchmark

New benchmarks are reported as "new, not in baseline" and are not gated until
the baseline is re-recorded. Two things to keep in mind:

- **Discard log output.** Use `benchLogger()` (`common.NewLoggerTo(level,
  io.Discard)`) rather than `common.NewLogger`. Log lines on stdout corrupt the
  gate's parsing, and stdout I/O would be charged to the measurement — while
  discarding still keeps the log *formatting* cost in it, which is where the
  real work is.
- **Fix your own concurrency.** Prefer an explicit worker count over
  `b.RunParallel` for anything load-related, so the number means the same thing
  on every machine.
