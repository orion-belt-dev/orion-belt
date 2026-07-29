package tracing

import (
	"context"

	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/trace"
	"golang.org/x/crypto/ssh"
)

// The gateway reaches an agent by opening an SSH "session" channel over the
// agent's reverse connection. That channel-open carries an arbitrary
// extra-data blob, which is the only inbound carrier available before any
// request is exchanged — so it is where W3C trace context rides across the
// gateway -> agent hop.
//
// Wire compatibility matters here because gateway and agents are upgraded
// independently:
//
//   - An agent that predates tracing ignores ExtraData entirely and accepts
//     the channel as before, so a new gateway talking to an old agent is safe.
//   - A new agent handed empty or unparseable data treats it as "no trace
//     context" and starts a local root span, so an old gateway talking to a
//     new agent is safe too.
//
// Neither direction can fail a session because of tracing, which is what lets
// this be enabled on one side at a time.

// channelTraceContext is the extra-data payload. Field order and types are the
// wire format; only append to it, never reorder or retype.
type channelTraceContext struct {
	TraceParent string
	TraceState  string
}

// InjectChannelData serializes the trace context from ctx into the blob to
// pass as SSH channel-open extra data.
//
// Returns nil when tracing is off or ctx carries no valid span, and callers
// pass nil through to OpenChannel unchanged — so a disabled gateway sends
// exactly the bytes it sent before tracing existed.
func InjectChannelData(ctx context.Context) []byte {
	if !enabled.Load() {
		return nil
	}
	if !trace.SpanContextFromContext(ctx).IsValid() {
		return nil
	}

	carrier := propagation.MapCarrier{}
	propagator.Inject(ctx, carrier)

	traceParent := carrier.Get("traceparent")
	if traceParent == "" {
		return nil
	}

	return ssh.Marshal(channelTraceContext{
		TraceParent: traceParent,
		TraceState:  carrier.Get("tracestate"),
	})
}

// ExtractChannelData returns ctx with any trace context found in the SSH
// channel-open extra data, making the agent's spans children of the gateway's.
//
// Malformed data is treated as absent rather than as an error: a
// trace-context problem must never be a reason to refuse a session.
func ExtractChannelData(ctx context.Context, data []byte) context.Context {
	if !enabled.Load() || len(data) == 0 {
		return ctx
	}

	var payload channelTraceContext
	if err := ssh.Unmarshal(data, &payload); err != nil {
		return ctx
	}
	if payload.TraceParent == "" {
		return ctx
	}

	carrier := propagation.MapCarrier{"traceparent": payload.TraceParent}
	if payload.TraceState != "" {
		carrier["tracestate"] = payload.TraceState
	}
	return propagator.Extract(ctx, carrier)
}

// HasRemoteParent reports whether ctx carries a span context extracted from
// another process. The agent uses it to tell a continued trace from one it
// rooted itself, which is the signal that the gateway hop was actually linked.
func HasRemoteParent(ctx context.Context) bool {
	sc := trace.SpanContextFromContext(ctx)
	return sc.IsValid() && sc.IsRemote()
}
