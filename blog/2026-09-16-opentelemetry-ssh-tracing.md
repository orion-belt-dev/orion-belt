---
slug: opentelemetry-ssh-tracing
title: Tracing SSH sessions with OpenTelemetry — finding the slow hop
description: How Orion Belt traces an SSH session across the gateway, the agent and the target with OpenTelemetry — the span tree, how W3C trace context crosses an SSH channel, what is kept out of spans, and how to turn it on.
authors: [orion]
tags: [observability, ssh, ops]
keywords: [OpenTelemetry SSH, trace SSH sessions, OTLP SSH gateway, SSH latency, distributed tracing bastion, traceparent SSH]
---

**When an SSH login through a gateway takes eight seconds, the question is
which hop is slow: authorization, the gateway → agent tunnel, or the agent →
target connection.** Logs and metrics don't answer that well. Distributed
tracing does. Since 1.1.0, Orion Belt emits OpenTelemetry traces for SSH
sessions across both processes.

{/* truncate */}

## Metrics, logs or traces?

| Question | Use |
| --- | --- |
| Is it up? How many sessions? Alert me | Metrics (`/metrics`, Prometheus) |
| What exactly happened in this session? | Logs and the session recording |
| **Which hop is slow or failing?** | **Traces** |

## The span tree

One trace per SSH session, spanning the gateway and the agent:

```text
gateway.ssh.session                (gateway, SERVER)
├── gateway.authorize              machine lookup + permission check
├── gateway.agent.open_channel     the gateway → agent hop (CLIENT)
│   └── agent.session              (agent, SERVER, same trace)
│       └── agent.target.shell     the agent → target hop
│           or agent.target.exec   one-shot command / scp
└── gateway.proxy.shell            session body
    or gateway.proxy.exec
```

Connect latency shows up in `gateway.authorize` and
`gateway.agent.open_channel`. The `proxy.*` and `target.shell` spans last as
long as the user stays connected: read them as session duration, not
latency.

Attributes to filter on: `orion.user`, `orion.target.machine`,
`orion.target.remote_user`, and `orion.session.id`, which joins a trace to
the session recording and the audit log.

## Carrying trace context through SSH

HTTP has headers for [W3C trace context](https://www.w3.org/TR/trace-context/).
SSH doesn't. The gateway puts the `traceparent` into the extra data of the
SSH channel-open request it sends to the agent, and the agent parents its
spans on it.

This is tolerant in both directions. An agent that predates tracing ignores
the extra data. A new agent that receives none starts its own trace and marks
it with `orion.trace.linked_to_gateway=false`. A trace-context problem never
fails a session, so you can upgrade gateway and agents separately. That
attribute is also how you spot a half-upgraded fleet.

## What stays out of spans

Spans go to a collector, which usually has weaker access controls than
session recordings. So commands are recorded as the program name only
(`orion.command`), never with arguments or `NAME=value` environment
prefixes. A `mysql -p...` typed by an admin doesn't end up in your tracing
backend.

## Turning it on

Both sides read the same block. You need both for a full trace.

```yaml
# server.yaml and agent.yaml
tracing:
  enabled: true
  endpoint: "otel-collector:4317"   # gRPC wants host:port, not a URL
  protocol: grpc                    # or http
  insecure: false                   # true only for a trusted plaintext link
  sample_ratio: 1.0
```

Leave `endpoint` empty to use the standard `OTEL_EXPORTER_OTLP_*`
environment variables instead. Any OTLP backend works: the OpenTelemetry
Collector, Jaeger, Grafana Tempo, Honeycomb.

Service names default to `orion-belt-gateway` and
`orion-belt-agent-<agent name>`. The agent name is included on purpose: a
fleet that all reports as one service makes the agent hop useless to look at.

## What it costs

Disabled means free: no exporter, no tracer provider, one atomic load per
entry point. Measured with the project's benchmarks on an M1:

| Path | Disabled | Enabled |
| --- | --- | --- |
| Start + End a span | 2.3 ns, 0 allocs | 644 ns, 3 allocs |

A test in CI asserts the disabled path doesn't allocate.

## Not traced (yet)

The HTTP API isn't instrumented; API requests carry a `request_id` for log
correlation instead. Web terminal sessions get the agent hop but no HTTP
parent span.

## FAQ

**Can OpenTelemetry trace an SSH connection?**
Not out of the box: SSH has no header for trace context. A gateway that
controls both ends can carry it, as Orion Belt does in the channel-open data.

**Does tracing record what users type?**
No. Spans hold timing, identities and the program name. Keystrokes and output
are in the session recording, which has its own access controls.

## Related

- [Orion Belt 1.1 and 1.2](/blog/orion-belt-v1-1-and-v1-2)
- [Why reverse SSH agents](/blog/why-reverse-ssh-agents)
