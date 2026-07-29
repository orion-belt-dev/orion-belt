// Package tracing provides optional OpenTelemetry distributed tracing across
// the gateway -> agent -> target request path, exported over OTLP.
//
// It complements the Prometheus counters in pkg/metrics and the JSON logs in
// pkg/common: metrics tell you that latency exists, traces tell you which hop
// it is in.
//
// Tracing is off unless explicitly enabled. When off, every entry point here
// short-circuits on a single atomic load before touching OpenTelemetry, no
// global TracerProvider is installed, and no exporter goroutines are started —
// so a deployment that never turns it on pays essentially nothing. See
// BenchmarkStartDisabled in the tests for the measured cost.
package tracing

import (
	"context"
	"fmt"
	"strings"
	"sync/atomic"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.41.0"
	"go.opentelemetry.io/otel/trace"
	"go.opentelemetry.io/otel/trace/noop"

	"github.com/zrougamed/orion-belt/pkg/common"
)

// Protocol values for Config.Protocol.
const (
	ProtocolGRPC = "grpc"
	ProtocolHTTP = "http"
)

// Config configures the exporter. The zero value is disabled.
type Config struct {
	// Enabled is the master switch. Everything else is ignored when false.
	Enabled bool

	// Endpoint is the OTLP collector address ("host:port" for gRPC, or a
	// "host:port" / URL for HTTP). Empty falls back to the standard
	// OTEL_EXPORTER_OTLP_* environment variables, so this works with a
	// collector configured the usual way.
	Endpoint string

	// Protocol selects the OTLP transport: "grpc" (default) or "http".
	Protocol string

	// Insecure disables TLS to the collector. Intended for a collector
	// running on localhost or inside a trusted network.
	Insecure bool

	// ServiceName identifies this process in the trace backend
	// ("orion-belt-gateway", "orion-belt-agent").
	ServiceName string

	// ServiceVersion is reported as service.version.
	ServiceVersion string

	// SampleRatio is the head-sampling probability for traces started by this
	// process, from 0.0 to 1.0. Unset (0) means 1.0 — a gateway handling
	// interactive SSH sessions produces few enough root spans that sampling
	// everything is the useful default. Sampling is parent-based, so a
	// decision made by the gateway is honored by the agent rather than
	// re-rolled, which is what keeps a multi-hop trace whole.
	SampleRatio float64

	// Headers are extra OTLP headers (e.g. an auth token for a hosted
	// collector).
	Headers map[string]string
}

// FromCommon converts the YAML-facing config into an exporter config.
func FromCommon(c common.TracingConfig, serviceName, serviceVersion string) Config {
	return Config{
		Enabled:        c.Enabled,
		Endpoint:       c.Endpoint,
		Protocol:       c.Protocol,
		Insecure:       c.Insecure,
		ServiceName:    firstNonEmpty(c.ServiceName, serviceName),
		ServiceVersion: serviceVersion,
		SampleRatio:    c.SampleRatio,
		Headers:        c.Headers,
	}
}

// enabled gates every hot-path entry point. It is the only thing consulted
// when tracing is off, which is what makes "disabled" cost an atomic load
// rather than an OpenTelemetry call chain.
var enabled atomic.Bool

// tracerRef holds the active tracer once Init succeeds. Reading it via
// atomic.Value avoids the map lookup and mutex inside otel.Tracer on every
// span start.
var tracerRef atomic.Value // trace.Tracer

// noopSpan is returned by Start when tracing is off. It is declared with the
// interface type rather than the concrete one on purpose: returning a concrete
// struct as trace.Span boxes it into an interface on every call, which showed
// up as an allocation per span in BenchmarkStartDisabled. Storing it
// pre-boxed makes the disabled path allocation-free.
var noopSpan trace.Span = noop.Span{}

// propagator is the wire format for cross-process context. Held as a package
// value rather than read from otel's global so that propagation behaves
// identically in tests that never call Init.
var propagator = propagation.TraceContext{}

// Enabled reports whether tracing is currently active. Call sites use it to
// skip building attributes that would be discarded.
func Enabled() bool { return enabled.Load() }

// ShutdownFunc flushes buffered spans and releases exporter resources.
type ShutdownFunc func(context.Context) error

// Init installs a global TracerProvider exporting over OTLP and returns a
// shutdown function that flushes pending spans.
//
// When cfg.Enabled is false this is a no-op: no provider is installed (so
// OpenTelemetry's built-in no-op implementation stays in place), no exporter
// connection is made, and the returned shutdown does nothing. Callers can
// therefore always defer the returned function without checking.
func Init(ctx context.Context, cfg Config, logger *common.Logger) (ShutdownFunc, error) {
	if !cfg.Enabled {
		return func(context.Context) error { return nil }, nil
	}

	if cfg.ServiceName == "" {
		return nil, fmt.Errorf("tracing: service name is required when tracing is enabled")
	}
	if cfg.SampleRatio < 0 || cfg.SampleRatio > 1 {
		return nil, fmt.Errorf("tracing: sample_ratio must be between 0.0 and 1.0, got %v", cfg.SampleRatio)
	}

	exporter, err := newExporter(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("tracing: create OTLP exporter: %w", err)
	}

	ratio := cfg.SampleRatio
	if ratio == 0 {
		ratio = 1.0
	}

	provider := sdktrace.NewTracerProvider(
		sdktrace.WithBatcher(exporter),
		sdktrace.WithResource(newResource(cfg)),
		// ParentBased means a sampling decision taken upstream is honored
		// here instead of being re-rolled, so an agent span cannot go missing
		// from a trace the gateway chose to keep.
		sdktrace.WithSampler(sdktrace.ParentBased(sdktrace.TraceIDRatioBased(ratio))),
	)

	otel.SetTracerProvider(provider)
	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(
		propagation.TraceContext{},
		propagation.Baggage{},
	))

	// A collector that is down must degrade the process to "no traces", never
	// to "broken gateway" — route exporter errors to the normal log instead of
	// OpenTelemetry's default stderr printer.
	if logger != nil {
		otel.SetErrorHandler(otel.ErrorHandlerFunc(func(err error) {
			logger.Warn("tracing exporter error: %v", err)
		}))
	}

	tracerRef.Store(provider.Tracer(cfg.ServiceName))
	enabled.Store(true)

	if logger != nil {
		logger.Info("Tracing enabled: service=%s protocol=%s endpoint=%s sample_ratio=%.2f",
			cfg.ServiceName, protocolOf(cfg), endpointForLog(cfg), ratio)
	}

	return func(shutdownCtx context.Context) error {
		// Flip the switch first so in-flight requests stop opening new spans
		// while the provider is draining.
		enabled.Store(false)
		return provider.Shutdown(shutdownCtx)
	}, nil
}

// newExporter builds the OTLP exporter for the configured protocol. An empty
// endpoint deliberately falls through to the exporter's own environment
// variable handling (OTEL_EXPORTER_OTLP_ENDPOINT and friends).
func newExporter(ctx context.Context, cfg Config) (*otlptrace.Exporter, error) {
	// Bound the initial connection attempt so a wrong endpoint fails startup
	// visibly rather than hanging the process.
	dialCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	switch protocolOf(cfg) {
	case ProtocolHTTP:
		opts := []otlptracehttp.Option{}
		if cfg.Endpoint != "" {
			opts = append(opts, otlptracehttp.WithEndpointURL(normalizeHTTPEndpoint(cfg)))
		}
		if cfg.Insecure {
			opts = append(opts, otlptracehttp.WithInsecure())
		}
		if len(cfg.Headers) > 0 {
			opts = append(opts, otlptracehttp.WithHeaders(cfg.Headers))
		}
		return otlptracehttp.New(dialCtx, opts...)

	case ProtocolGRPC:
		opts := []otlptracegrpc.Option{}
		if cfg.Endpoint != "" {
			opts = append(opts, otlptracegrpc.WithEndpoint(cfg.Endpoint))
		}
		if cfg.Insecure {
			opts = append(opts, otlptracegrpc.WithInsecure())
		}
		if len(cfg.Headers) > 0 {
			opts = append(opts, otlptracegrpc.WithHeaders(cfg.Headers))
		}
		return otlptracegrpc.New(dialCtx, opts...)

	default:
		return nil, fmt.Errorf("tracing: unknown protocol %q (want %q or %q)", cfg.Protocol, ProtocolGRPC, ProtocolHTTP)
	}
}

// protocolOf resolves the configured protocol, defaulting to gRPC.
func protocolOf(cfg Config) string {
	p := strings.ToLower(strings.TrimSpace(cfg.Protocol))
	if p == "" {
		return ProtocolGRPC
	}
	return p
}

// normalizeHTTPEndpoint accepts a bare "host:port" as well as a full URL, so
// the same config value shape works for both protocols.
func normalizeHTTPEndpoint(cfg Config) string {
	ep := strings.TrimSpace(cfg.Endpoint)
	if strings.HasPrefix(ep, "http://") || strings.HasPrefix(ep, "https://") {
		return ep
	}
	if cfg.Insecure {
		return "http://" + ep
	}
	return "https://" + ep
}

func endpointForLog(cfg Config) string {
	if cfg.Endpoint == "" {
		return "(from OTEL_EXPORTER_OTLP_* environment)"
	}
	return cfg.Endpoint
}

// newResource describes this process to the trace backend. Merging over
// resource.Default keeps the SDK's own telemetry.* attributes alongside ours.
func newResource(cfg Config) *resource.Resource {
	attrs := []attribute.KeyValue{semconv.ServiceName(cfg.ServiceName)}
	if cfg.ServiceVersion != "" {
		attrs = append(attrs, semconv.ServiceVersion(cfg.ServiceVersion))
	}
	own := resource.NewWithAttributes(semconv.SchemaURL, attrs...)

	merged, err := resource.Merge(resource.Default(), own)
	if err != nil {
		// Merge only fails on conflicting schema URLs. Our own attributes are
		// the ones that matter for identifying the service, so keep them
		// rather than dropping the resource entirely.
		return own
	}
	return merged
}

// Tracer returns the active tracer, or a no-op tracer when tracing is off.
func Tracer() trace.Tracer {
	if t, ok := tracerRef.Load().(trace.Tracer); ok && t != nil {
		return t
	}
	return noop.NewTracerProvider().Tracer("")
}

// Start begins a span, or returns the context unchanged and a no-op span when
// tracing is disabled.
//
// The disabled path costs one atomic load and no allocation. Prefer passing
// attributes via span.SetAttributes guarded by IsRecording (or by Enabled)
// when building them is not free — arguments to this function are evaluated by
// the caller whether tracing is on or not.
func Start(ctx context.Context, name string, opts ...trace.SpanStartOption) (context.Context, trace.Span) {
	if !enabled.Load() {
		return ctx, noopSpan
	}
	return Tracer().Start(ctx, name, opts...)
}

// SetAttributes attaches attributes to a span only if it is actually
// recording, so callers can build them inline without a manual guard.
func SetAttributes(span trace.Span, attrs ...attribute.KeyValue) {
	if span == nil || !span.IsRecording() {
		return
	}
	span.SetAttributes(attrs...)
}

// RecordError marks a span failed. Safe to call with a nil error (no-op) so
// call sites can hand it a result without branching.
func RecordError(span trace.Span, err error) {
	if span == nil || err == nil || !span.IsRecording() {
		return
	}
	span.RecordError(err)
	span.SetStatus(codes.Error, err.Error())
}

// CommandName reduces a command line to its program name, for use as a span
// attribute.
//
// Arguments are dropped deliberately and this is the only sanctioned way to
// put a command on a span: a full command line routinely carries passwords,
// tokens, and file paths, and spans are shipped off-box to a collector that
// does not inherit the access controls guarding session recordings.
func CommandName(command string) string {
	command = strings.TrimSpace(command)
	if i := strings.IndexByte(command, ' '); i >= 0 {
		return command[:i]
	}
	return command
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}
