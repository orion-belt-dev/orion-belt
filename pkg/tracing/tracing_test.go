package tracing

import (
	"context"
	"strings"
	"testing"

	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace"

	"github.com/orion-belt-dev/orion-belt/pkg/common"
)

// withRecordingTracer turns tracing on against an in-memory exporter and
// restores the previous state afterwards, so tests can exercise the enabled
// path without an OTLP collector.
func withRecordingTracer(t *testing.T) *tracetest.SpanRecorder {
	t.Helper()

	recorder := tracetest.NewSpanRecorder()
	provider := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(recorder))

	prevEnabled := enabled.Load()
	prevTracer := tracerRef.Load()

	tracerRef.Store(provider.Tracer("test"))
	enabled.Store(true)

	t.Cleanup(func() {
		enabled.Store(prevEnabled)
		if prevTracer != nil {
			tracerRef.Store(prevTracer)
		}
		_ = provider.Shutdown(context.Background())
	})

	return recorder
}

// withTracingDisabled forces the disabled state regardless of what other tests
// left behind.
func withTracingDisabled(t *testing.T) {
	t.Helper()
	prev := enabled.Load()
	enabled.Store(false)
	t.Cleanup(func() { enabled.Store(prev) })
}

// AC3: disabled tracing must not install a provider, must not create spans,
// and must hand back the caller's own context untouched.
func TestDisabledStartIsInert(t *testing.T) {
	withTracingDisabled(t)

	ctx := context.Background()
	got, span := Start(ctx, "should.not.exist")

	if got != ctx {
		t.Error("disabled Start must return the caller's context unchanged")
	}
	if span.IsRecording() {
		t.Error("disabled Start must not return a recording span")
	}
	if span.SpanContext().IsValid() {
		t.Error("disabled Start must not produce a valid span context")
	}
	if Enabled() {
		t.Error("Enabled must report false")
	}

	// Must be safe to use like any other span.
	SetAttributes(span)
	RecordError(span, context.Canceled)
	span.End()
}

// Init with Enabled=false must not touch OpenTelemetry at all, and must still
// return a usable shutdown so callers can defer unconditionally.
func TestInitDisabledIsNoOp(t *testing.T) {
	withTracingDisabled(t)

	shutdown, err := Init(context.Background(), Config{Enabled: false}, nil)
	if err != nil {
		t.Fatalf("disabled Init should not fail: %v", err)
	}
	if shutdown == nil {
		t.Fatal("disabled Init must still return a shutdown func")
	}
	if err := shutdown(context.Background()); err != nil {
		t.Errorf("disabled shutdown should not fail: %v", err)
	}
	if Enabled() {
		t.Error("disabled Init must leave tracing off")
	}
}

// Enabling without a service name would produce unattributable traces, so it
// is rejected at startup rather than discovered in the backend later.
func TestInitRejectsMissingServiceName(t *testing.T) {
	withTracingDisabled(t)

	if _, err := Init(context.Background(), Config{Enabled: true}, nil); err == nil {
		t.Fatal("expected an error when service name is empty")
	}
	if Enabled() {
		t.Error("a failed Init must not leave tracing enabled")
	}
}

func TestInitRejectsOutOfRangeSampleRatio(t *testing.T) {
	withTracingDisabled(t)

	for _, ratio := range []float64{-0.1, 1.5} {
		cfg := Config{Enabled: true, ServiceName: "svc", SampleRatio: ratio}
		if _, err := Init(context.Background(), cfg, nil); err == nil {
			t.Errorf("expected sample ratio %v to be rejected", ratio)
		}
	}
}

func TestInitRejectsUnknownProtocol(t *testing.T) {
	withTracingDisabled(t)

	cfg := Config{Enabled: true, ServiceName: "svc", Protocol: "carrier-pigeon"}
	if _, err := Init(context.Background(), cfg, nil); err == nil {
		t.Fatal("expected an unknown OTLP protocol to be rejected")
	}
}

func TestInitRejectsHTTPPlaintextWhenSecure(t *testing.T) {
	withTracingDisabled(t)

	cfg := Config{
		Enabled:     true,
		ServiceName: "svc",
		Protocol:    ProtocolHTTP,
		Endpoint:    "http://collector:4318",
		Insecure:    false,
	}
	if _, err := Init(context.Background(), cfg, nil); err == nil {
		t.Fatal("expected http:// with insecure:false to be rejected")
	}
}

func TestInitRejectsGRPCURLEndpoint(t *testing.T) {
	withTracingDisabled(t)

	cfg := Config{
		Enabled:     true,
		ServiceName: "svc",
		Protocol:    ProtocolGRPC,
		Endpoint:    "http://collector:4317",
		Insecure:    true,
	}
	if _, err := Init(context.Background(), cfg, nil); err == nil {
		t.Fatal("expected a URL-shaped gRPC endpoint to be rejected")
	}
}

func TestInitRejectsDoubleInit(t *testing.T) {
	withTracingDisabled(t)

	// Point at a reserved TEST-NET address so we never need a real collector.
	cfg := Config{
		Enabled:     true,
		ServiceName: "svc",
		Protocol:    ProtocolHTTP,
		Endpoint:    "http://192.0.2.1:4318",
		Insecure:    true,
	}
	shutdown, err := Init(context.Background(), cfg, nil)
	if err != nil {
		t.Fatalf("first Init: %v", err)
	}
	t.Cleanup(func() {
		_ = shutdown(context.Background())
	})

	if _, err := Init(context.Background(), cfg, nil); err == nil {
		t.Fatal("expected a second Init before Shutdown to fail")
	}
}

// When enabled, spans must actually be produced and carry their attributes.
func TestEnabledStartRecordsSpan(t *testing.T) {
	recorder := withRecordingTracer(t)

	_, span := Start(context.Background(), "gateway.ssh.session", trace.WithSpanKind(trace.SpanKindServer))
	if !span.IsRecording() {
		t.Fatal("expected a recording span when tracing is enabled")
	}
	span.End()

	ended := recorder.Ended()
	if len(ended) != 1 {
		t.Fatalf("expected 1 span, got %d", len(ended))
	}
	if ended[0].Name() != "gateway.ssh.session" {
		t.Errorf("unexpected span name %q", ended[0].Name())
	}
	if ended[0].SpanKind() != trace.SpanKindServer {
		t.Errorf("unexpected span kind %v", ended[0].SpanKind())
	}
}

// Child spans must land in the same trace, which is what makes a multi-hop
// path readable as one thing.
func TestChildSpanSharesTrace(t *testing.T) {
	recorder := withRecordingTracer(t)

	ctx, parent := Start(context.Background(), "gateway.ssh.session")
	_, child := Start(ctx, "gateway.authorize")
	child.End()
	parent.End()

	ended := recorder.Ended()
	if len(ended) != 2 {
		t.Fatalf("expected 2 spans, got %d", len(ended))
	}
	if ended[0].SpanContext().TraceID() != ended[1].SpanContext().TraceID() {
		t.Error("child and parent must share a trace ID")
	}
	if ended[0].Parent().SpanID() != ended[1].SpanContext().SpanID() {
		t.Error("child's parent must be the outer span")
	}
}

// RecordError must be safe on the paths that call it with a possibly-nil error.
func TestRecordErrorIgnoresNil(t *testing.T) {
	recorder := withRecordingTracer(t)

	_, span := Start(context.Background(), "op")
	RecordError(span, nil)
	span.End()

	if got := len(recorder.Ended()[0].Events()); got != 0 {
		t.Errorf("a nil error must not record an event, got %d", got)
	}
}

func TestRecordErrorOnNilSpanDoesNotPanic(t *testing.T) {
	RecordError(nil, context.Canceled)
	SetAttributes(nil)
}

// FromCommon is the bridge between the YAML config and the exporter; a wrong
// mapping here silently disables or misdirects tracing.
func TestFromCommonMapsConfig(t *testing.T) {
	got := FromCommon(common.TracingConfig{
		Enabled:     true,
		Endpoint:    "collector:4317",
		Protocol:    "http",
		Insecure:    true,
		SampleRatio: 0.25,
		Headers:     map[string]string{"authorization": "Bearer x"},
	}, "default-name", "1.2.3")

	if !got.Enabled || got.Endpoint != "collector:4317" || got.Protocol != "http" || !got.Insecure {
		t.Errorf("transport fields not mapped: %+v", got)
	}
	if got.SampleRatio != 0.25 {
		t.Errorf("sample ratio = %v", got.SampleRatio)
	}
	if got.ServiceName != "default-name" {
		t.Errorf("expected the fallback service name, got %q", got.ServiceName)
	}
	if got.ServiceVersion != "1.2.3" {
		t.Errorf("service version = %q", got.ServiceVersion)
	}
	if got.Headers["authorization"] != "Bearer x" {
		t.Errorf("headers not mapped: %v", got.Headers)
	}
}

// An explicit service name in config must win over the per-binary default.
func TestFromCommonServiceNameOverride(t *testing.T) {
	got := FromCommon(common.TracingConfig{ServiceName: "custom"}, "default-name", "")
	if got.ServiceName != "custom" {
		t.Errorf("expected the configured name to win, got %q", got.ServiceName)
	}
}

func TestNormalizeHTTPEndpoint(t *testing.T) {
	tests := []struct {
		name string
		cfg  Config
		want string
	}{
		{"bare host insecure", Config{Endpoint: "collector:4318", Insecure: true}, "http://collector:4318"},
		{"bare host secure", Config{Endpoint: "collector:4318"}, "https://collector:4318"},
		{"explicit http url kept", Config{Endpoint: "http://collector:4318", Insecure: true}, "http://collector:4318"},
		{"explicit https url kept", Config{Endpoint: "https://collector:4318"}, "https://collector:4318"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := normalizeHTTPEndpoint(tc.cfg); got != tc.want {
				t.Errorf("got %q, want %q", got, tc.want)
			}
		})
	}
}

func TestProtocolDefaultsToGRPC(t *testing.T) {
	if got := protocolOf(Config{}); got != ProtocolGRPC {
		t.Errorf("empty protocol should default to grpc, got %q", got)
	}
	if got := protocolOf(Config{Protocol: "  HTTP "}); got != ProtocolHTTP {
		t.Errorf("protocol should be normalized, got %q", got)
	}
}

// CommandName is the single guard stopping command-line secrets from reaching
// a trace collector, so it gets its own tests rather than relying on the call
// sites to be careful.
func TestCommandNameStripsArguments(t *testing.T) {
	tests := []struct {
		name    string
		command string
		want    string
	}{
		{"bare program", "ls", "ls"},
		{"drops arguments", "ls -la /etc", "ls"},
		{"trims surrounding space", "  whoami  ", "whoami"},
		{"empty stays empty", "", ""},
		{"only whitespace", "   ", ""},
		{"absolute path kept", "/usr/bin/scp -t /tmp", "/usr/bin/scp"},
		// The cases that actually matter: none of these may survive.
		{"password argument dropped", "mysql -u root -phunter2", "mysql"},
		{"token argument dropped", "curl -H 'Authorization: Bearer sk-secret'", "curl"},
		{"env-style secret dropped", "deploy --api-key=AKIAIOSFODNN7EXAMPLE", "deploy"},
		// Env-var assignment prefixes must not be returned as the "program".
		{"leading env assignment", "PGPASSWORD=hunter2 psql -h db", "psql"},
		{"leading aws env", "AWS_SECRET_ACCESS_KEY=abc123 aws s3 ls", "aws"},
		{"multiple leading envs", "FOO=1 BAR=2 true", "true"},
		{"only env assignments", "FOO=secret BAR=other", ""},
		{"empty env value", "FOO= psql", "psql"},
		// Non-space whitespace must still split — a tab must not ship the
		// whole line (including query tokens) as the program name.
		{"tab separator", "curl\thttps://x.example/?token=abc", "curl"},
		{"newline separator", "curl\nhttps://x.example/?token=abc", "curl"},
		{"caps long name", strings.Repeat("a", maxCommandNameLen+10), strings.Repeat("a", maxCommandNameLen)},
		{"not an env prefix", "1=foo ls", "1=foo"}, // leading digit is not a NAME
		{"equals in path kept when sole field", "/tmp/a=b", "/tmp/a=b"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := CommandName(tc.command)
			if got != tc.want {
				t.Errorf("CommandName(%q) = %q, want %q", tc.command, got, tc.want)
			}
			if strings.ContainsAny(got, " \t\n\r") {
				t.Errorf("result %q still contains whitespace", got)
			}
		})
	}
}

// AC3, as a test rather than a benchmark: the disabled path must not allocate.
//
// This is easy to regress by accident — returning a concrete no-op span
// instead of a pre-boxed interface value costs an allocation per span, which
// on the SSH session path means garbage for every connection on deployments
// that never enabled tracing. Asserting zero here catches that in CI rather
// than in someone's heap profile.
func TestDisabledPathDoesNotAllocate(t *testing.T) {
	withTracingDisabled(t)

	ctx := context.Background()

	if got := testing.AllocsPerRun(1000, func() {
		_, span := Start(ctx, "gateway.ssh.session")
		span.End()
	}); got != 0 {
		t.Errorf("disabled Start allocated %v times per run, want 0", got)
	}

	if got := testing.AllocsPerRun(1000, func() {
		_ = InjectChannelData(ctx)
	}); got != 0 {
		t.Errorf("disabled InjectChannelData allocated %v times per run, want 0", got)
	}

	// Built outside the closure: a []byte(string) conversion allocates on its
	// own and would be measured as though it came from the call.
	payload := []byte("ignored while disabled")
	if got := testing.AllocsPerRun(1000, func() {
		_ = ExtractChannelData(ctx, payload)
	}); got != 0 {
		t.Errorf("disabled ExtractChannelData allocated %v times per run, want 0", got)
	}
}

// AC3: the disabled path must cost essentially nothing. Compare against the
// enabled path in the same run so the numbers are meaningful on any machine.
//
// Run: go test ./pkg/tracing/ -bench 'BenchmarkStart' -benchmem
func BenchmarkStartDisabled(b *testing.B) {
	prev := enabled.Load()
	enabled.Store(false)
	b.Cleanup(func() { enabled.Store(prev) })

	ctx := context.Background()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, span := Start(ctx, "gateway.ssh.session")
		span.End()
	}
}

func BenchmarkStartEnabled(b *testing.B) {
	recorder := tracetest.NewSpanRecorder()
	provider := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(recorder))

	prevEnabled := enabled.Load()
	prevTracer := tracerRef.Load()
	tracerRef.Store(provider.Tracer("bench"))
	enabled.Store(true)
	b.Cleanup(func() {
		enabled.Store(prevEnabled)
		if prevTracer != nil {
			tracerRef.Store(prevTracer)
		}
		_ = provider.Shutdown(context.Background())
	})

	ctx := context.Background()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, span := Start(ctx, "gateway.ssh.session")
		span.End()
	}
}

// The injection path runs on every session, so its disabled cost matters too.
func BenchmarkInjectChannelDataDisabled(b *testing.B) {
	prev := enabled.Load()
	enabled.Store(false)
	b.Cleanup(func() { enabled.Store(prev) })

	ctx := context.Background()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = InjectChannelData(ctx)
	}
}
