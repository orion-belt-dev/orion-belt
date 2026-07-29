package tracing

import (
	"context"
	"testing"

	"go.opentelemetry.io/otel/trace"
	"golang.org/x/crypto/ssh"
)

// The whole point of AC1: a span started on the agent from injected gateway
// data must land in the gateway's trace, not a new one.
func TestInjectExtractLinksGatewayToAgent(t *testing.T) {
	recorder := withRecordingTracer(t)

	// Gateway side.
	gatewayCtx, gatewaySpan := Start(context.Background(), "gateway.agent.open_channel",
		trace.WithSpanKind(trace.SpanKindClient))
	data := InjectChannelData(gatewayCtx)
	if len(data) == 0 {
		t.Fatal("expected trace context to be injected")
	}

	// Agent side, in what would be a different process.
	agentCtx := ExtractChannelData(context.Background(), data)
	if !HasRemoteParent(agentCtx) {
		t.Fatal("extracted context should carry a remote parent")
	}
	_, agentSpan := Start(agentCtx, "agent.session", trace.WithSpanKind(trace.SpanKindServer))
	agentSpan.End()
	gatewaySpan.End()

	ended := recorder.Ended()
	if len(ended) != 2 {
		t.Fatalf("expected 2 spans, got %d", len(ended))
	}
	agent, gateway := ended[0], ended[1]

	if agent.SpanContext().TraceID() != gateway.SpanContext().TraceID() {
		t.Error("agent span must share the gateway's trace ID")
	}
	if agent.Parent().SpanID() != gateway.SpanContext().SpanID() {
		t.Error("agent span's parent must be the gateway's channel-open span")
	}
	if !agent.Parent().IsRemote() {
		t.Error("agent span's parent must be marked remote")
	}
}

// The sampling decision has to survive the hop, or a sampled gateway session
// would show up with its agent half missing.
func TestInjectPreservesSampledFlag(t *testing.T) {
	withRecordingTracer(t)

	ctx, span := Start(context.Background(), "gateway.agent.open_channel")
	defer span.End()

	if !span.SpanContext().IsSampled() {
		t.Fatal("expected the test tracer to sample")
	}

	extracted := ExtractChannelData(context.Background(), InjectChannelData(ctx))
	if !trace.SpanContextFromContext(extracted).IsSampled() {
		t.Error("sampled flag must survive the gateway -> agent hop")
	}
}

// Compatibility: a gateway that predates tracing sends no extra data. A new
// agent must treat that as "no context" and carry on.
func TestExtractHandlesAbsentData(t *testing.T) {
	withRecordingTracer(t)

	ctx := context.Background()
	for _, data := range [][]byte{nil, {}} {
		got := ExtractChannelData(ctx, data)
		if got != ctx {
			t.Error("absent data must return the context unchanged")
		}
		if HasRemoteParent(got) {
			t.Error("absent data must not yield a remote parent")
		}
	}
}

// A trace-context problem must never be a reason to refuse a session, so
// garbage on the wire is treated as absent rather than as an error.
func TestExtractHandlesMalformedData(t *testing.T) {
	withRecordingTracer(t)

	ctx := context.Background()
	malformed := [][]byte{
		[]byte("not ssh-marshalled at all"),
		{0xff, 0xff, 0xff, 0xff},
		{0x00},
	}

	for _, data := range malformed {
		got := ExtractChannelData(ctx, data)
		if HasRemoteParent(got) {
			t.Errorf("malformed data %v must not produce a remote parent", data)
		}
	}
}

// An ssh-marshalled payload with an empty traceparent is well-formed but
// carries nothing; it must not be mistaken for a valid parent.
func TestExtractHandlesEmptyTraceParent(t *testing.T) {
	withRecordingTracer(t)

	data := ssh.Marshal(channelTraceContext{})
	if HasRemoteParent(ExtractChannelData(context.Background(), data)) {
		t.Error("an empty traceparent must not produce a remote parent")
	}
}

// Compatibility the other way: a gateway with tracing off must send exactly
// the bytes it sent before tracing existed, so an old agent sees no change.
func TestInjectReturnsNilWhenDisabled(t *testing.T) {
	withTracingDisabled(t)

	if got := InjectChannelData(context.Background()); got != nil {
		t.Errorf("disabled inject must return nil, got %v", got)
	}
}

// With no span in flight there is nothing to propagate, so nothing should be
// put on the wire.
func TestInjectReturnsNilWithoutSpan(t *testing.T) {
	withRecordingTracer(t)

	if got := InjectChannelData(context.Background()); got != nil {
		t.Errorf("expected nil without an active span, got %v", got)
	}
}

// An agent with tracing off should not pay to parse context it cannot use.
func TestExtractIsInertWhenDisabled(t *testing.T) {
	withRecordingTracer(t)
	ctx, span := Start(context.Background(), "gateway.agent.open_channel")
	data := InjectChannelData(ctx)
	span.End()

	withTracingDisabled(t)

	base := context.Background()
	if got := ExtractChannelData(base, data); got != base {
		t.Error("disabled extract must return the context unchanged")
	}
}

// Tracestate is part of W3C context and vendors rely on it surviving hops.
func TestTraceStateSurvivesRoundTrip(t *testing.T) {
	withRecordingTracer(t)

	ts, err := trace.ParseTraceState("vendor=value")
	if err != nil {
		t.Fatalf("parse tracestate: %v", err)
	}
	sc := trace.NewSpanContext(trace.SpanContextConfig{
		TraceID:    trace.TraceID{0x01, 0x02, 0x03, 0x04, 0x05, 0x06, 0x07, 0x08, 0x09, 0x0a, 0x0b, 0x0c, 0x0d, 0x0e, 0x0f, 0x10},
		SpanID:     trace.SpanID{0x01, 0x02, 0x03, 0x04, 0x05, 0x06, 0x07, 0x08},
		TraceFlags: trace.FlagsSampled,
		TraceState: ts,
	})
	ctx := trace.ContextWithSpanContext(context.Background(), sc)

	extracted := ExtractChannelData(context.Background(), InjectChannelData(ctx))
	got := trace.SpanContextFromContext(extracted)

	if got.TraceID() != sc.TraceID() {
		t.Errorf("trace ID = %v, want %v", got.TraceID(), sc.TraceID())
	}
	if got.TraceState().Get("vendor") != "value" {
		t.Errorf("tracestate lost: %q", got.TraceState().String())
	}
}
