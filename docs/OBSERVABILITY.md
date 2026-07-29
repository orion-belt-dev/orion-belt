# Observability

JSON logs on stdout, Prometheus text at `/metrics`, a `request_id` on HTTP requests so you can glue events together, and optional OTLP distributed tracing across the gateway → agent → target path.

Rough guide to which one answers what:

| Question | Use |
| --- | --- |
| Is it up, how many sessions, alert me | Metrics (`/metrics`) |
| What exactly happened in this session | Logs + session recording |
| **Which hop is slow or failing** | **Traces** |

## Tracing (OTLP)

Off by default. When enabled, the gateway and agent emit spans over OTLP to any standard collector (OpenTelemetry Collector, Jaeger, Tempo, Honeycomb, …).

### Turning it on

Both sides read the same `tracing:` block, and each can be enabled independently — though you need both for a trace to cover the whole path.

```yaml
# server.yaml and agent.yaml
tracing:
  enabled: true
  endpoint: "otel-collector:4317"   # empty = use OTEL_EXPORTER_OTLP_* env vars
  protocol: grpc                    # grpc (default) | http
  insecure: true                    # plaintext to a collector on localhost / trusted network
  sample_ratio: 1.0                 # 0.0–1.0; unset means 1.0
  service_name: ""                  # optional override
  headers:                          # optional, e.g. a hosted collector's auth
    authorization: "Bearer ..."
```

Leave `endpoint` empty to configure the collector the standard way instead:

```bash
export OTEL_EXPORTER_OTLP_ENDPOINT=http://otel-collector:4317
```

Service names default to `orion-belt-gateway` and `orion-belt-agent-<agent name>`. The agent's name is included because a fleet all reporting as one service makes the agent hop useless to look at.

### What you get

One trace per SSH session, spanning both processes:

```
gateway.ssh.session                (gateway, SERVER)
├── gateway.authorize              machine lookup + permission check
├── gateway.agent.open_channel     the gateway → agent hop (CLIENT)
│   └── agent.session              (agent, SERVER — continues the same trace)
│       └── agent.target.shell     the agent → target hop
│           or agent.target.exec   one-shot command / scp
└── gateway.proxy.shell            session body
    or gateway.proxy.exec
```

Connect-time latency shows up in `gateway.authorize` and `gateway.agent.open_channel`. The `proxy.*` and `target.shell` spans last as long as the user stays connected, so read them as session duration, not latency.

Useful attributes: `orion.user`, `orion.target.machine`, `orion.target.remote_user`, `orion.session.id` (joins to the session recording and audit log), and `orion.trace.linked_to_gateway` on the agent side.

### How context crosses the hop

The gateway injects W3C `traceparent` into the SSH channel-open extra data; the agent extracts it and parents its spans on it. This is version-tolerant in both directions — an agent that predates tracing ignores the extra data, and a new agent handed none simply starts its own trace. A trace-context problem never fails a session, so you can roll the two sides out separately.

`orion.trace.linked_to_gateway=false` on agent spans is the signal that a session reached an agent without gateway context — usually a half-upgraded fleet or tracing enabled on only one side.

### What is not traced

The HTTP API is not instrumented; API requests still carry `request_id` for log correlation. Web-terminal sessions get the agent hop (via `gateway.agent.open_channel`) but no HTTP-side parent span, so they appear as their own trace rather than one rooted at the HTTP request.

### Secrets

Command lines are recorded as the program name only (`orion.command`), never with arguments — spans go off-box to a collector that does not inherit the access controls guarding session recordings. If you add instrumentation, route any command through `tracing.CommandName`.

### Cost when disabled

Disabled is genuinely free, not merely cheap: no exporter is created, no TracerProvider is installed, and each entry point returns on a single atomic load. Measured on an M1 (`go test ./pkg/tracing/ -bench . -benchmem`):

| Path | Disabled | Enabled |
| --- | --- | --- |
| `Start` + `End` | 2.3 ns, 0 allocs | 644 ns, 3 allocs |
| `InjectChannelData` | 2.1 ns, 0 allocs | — |

`TestDisabledPathDoesNotAllocate` asserts the zero-allocation property in CI so it cannot regress quietly.

## Logs → Loki / ELK

One JSON object per line. You’ll usually see `time`, `level`, `msg`, and often `request_id`.

### Promtail / Alloy sketch

```yaml
scrape_configs:
  - job_name: orion-belt
    static_configs:
      - targets: [localhost]
        labels:
          job: orion-belt
          __path__: /var/log/orion-belt/*.log
    pipeline_stages:
      - json:
          expressions:
            level: level
            msg: msg
            request_id: request_id
      - labels:
          level:
          request_id:
```

If you run under systemd, scrape the `orion-belt-server` unit and parse the JSON payload the same way.

### Elasticsearch

Filebeat with `json.keys_under_root: true` (or the Elastic Agent equivalent) is enough. Indexing on `request_id` and `level` helps.

## Metrics

Scrape `/metrics`. Counters/gauges include:

- `orion_belt_up`
- `orion_belt_uptime_seconds`
- `orion_belt_ssh_sessions_total` / `orion_belt_ssh_sessions_active`
- `orion_belt_api_requests_total`
- `orion_belt_auth_failures_total`
- `orion_belt_access_requests_total`
- `orion_belt_agents_connected`

## Operational dashboard snapshot API

For day-to-day operational visibility in the console, Orion Belt exposes a rolling analytics snapshot at:

- `GET /api/v1/dashboard/usage?window_hours=24&top=5`

It returns access volume, approval latency (avg/p50/p95), and most-accessed targets for the requested window. The web dashboard polls this endpoint periodically, so operators do not need to generate a report manually.

## Example alerts

Drop-in file: `deploy/prometheus/orion-belt-alerts.yml` — down instance, auth failure spike, no agents, silly number of active sessions. Point Alertmanager (or Grafana) at whatever you already use.


