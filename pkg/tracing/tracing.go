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
	"sync"
	"sync/atomic"
	"time"
	"unicode"

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

// maxCommandNameLen caps the program name written to orion.command so a
// pathological absolute path cannot flood the collector.
const maxCommandNameLen = 128

// exporterShutdownGrace is how long we give the exporter to release its
// connection after the batch processor's Shutdown has already timed out.
const exporterShutdownGrace = 2 * time.Second

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

// initMu serializes Init/shutdown so a second Init cannot replace the active
// provider while the first is still live (which would leak its goroutines).
var initMu sync.Mutex

// initialized is true between a successful enabled Init and its ShutdownFunc.
var initialized bool

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
//
// Init only validates config and constructs the exporter client — OTLP New is
// non-blocking, so a typo'd or unreachable endpoint does not fail startup.
// Export errors are routed to the logger instead. A second Init before the
// previous ShutdownFunc runs returns an error rather than leaking the old
// provider.
func Init(ctx context.Context, cfg Config, logger *common.Logger) (ShutdownFunc, error) {
	if !cfg.Enabled {
		return func(context.Context) error { return nil }, nil
	}

	if err := validateConfig(cfg); err != nil {
		return nil, err
	}

	initMu.Lock()
	defer initMu.Unlock()
	if initialized {
		return nil, fmt.Errorf("tracing: already initialized; call the previous ShutdownFunc first")
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
	initialized = true

	if logger != nil {
		logger.Info("Tracing enabled: service=%s protocol=%s endpoint=%s sample_ratio=%.2f",
			cfg.ServiceName, protocolOf(cfg), endpointForLog(cfg), ratio)
	}

	var shutdownOnce sync.Once
	var shutdownErr error
	return func(shutdownCtx context.Context) error {
		shutdownOnce.Do(func() {
			// Flip the switch first so in-flight requests stop opening new spans
			// while the provider is draining.
			enabled.Store(false)
			shutdownErr = provider.Shutdown(shutdownCtx)
			if shutdownErr != nil {
				// BatchSpanProcessor.Shutdown aborts on ctx cancel before it
				// reaches exporter.Shutdown, leaving the gRPC ClientConn and
				// retry goroutines alive. Force the exporter closed so a
				// timed-out flush actually releases resources.
				forceCtx, cancel := context.WithTimeout(context.Background(), exporterShutdownGrace)
				_ = exporter.Shutdown(forceCtx)
				cancel()
			}
			initMu.Lock()
			initialized = false
			initMu.Unlock()
		})
		return shutdownErr
	}, nil
}

// validateConfig rejects combinations that would silently misbehave: missing
// identity, bad sample ratio, unknown protocol, plaintext despite insecure:
// false, and URL-shaped gRPC endpoints that WithEndpoint cannot dial.
func validateConfig(cfg Config) error {
	if cfg.ServiceName == "" {
		return fmt.Errorf("tracing: service name is required when tracing is enabled")
	}
	if cfg.SampleRatio < 0 || cfg.SampleRatio > 1 {
		return fmt.Errorf("tracing: sample_ratio must be between 0.0 and 1.0, got %v", cfg.SampleRatio)
	}

	protocol := protocolOf(cfg)
	switch protocol {
	case ProtocolHTTP, ProtocolGRPC:
	default:
		return fmt.Errorf("tracing: unknown protocol %q (want %q or %q)", cfg.Protocol, ProtocolGRPC, ProtocolHTTP)
	}

	ep := strings.TrimSpace(cfg.Endpoint)
	if protocol == ProtocolHTTP && !cfg.Insecure {
		if strings.HasPrefix(ep, "http://") {
			return fmt.Errorf("tracing: endpoint %q uses http:// but insecure is false; set insecure: true for plaintext or use https://", ep)
		}
	}
	if protocol == ProtocolGRPC && ep != "" {
		if strings.Contains(ep, "://") {
			return fmt.Errorf("tracing: gRPC endpoint must be host:port, not a URL (%q)", ep)
		}
	}
	return nil
}

// newExporter builds the OTLP exporter for the configured protocol. An empty
// endpoint deliberately falls through to the exporter's own environment
// variable handling (OTEL_EXPORTER_OTLP_ENDPOINT and friends).
//
// Both otlptracegrpc.New and otlptracehttp.New are non-blocking: they build a
// client that connects lazily on the first export. A wrong endpoint therefore
// does not fail Init — it fails later as export errors routed to the logger.
func newExporter(ctx context.Context, cfg Config) (*otlptrace.Exporter, error) {
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
		return otlptracehttp.New(ctx, opts...)

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
		return otlptracegrpc.New(ctx, opts...)

	default:
		// validateConfig already rejected unknown protocols; keep the default
		// arm so a future caller of newExporter alone still gets a clear error.
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
//
// Leading NAME=value tokens (the "VAR=secret cmd" shape common over
// non-interactive SSH) are skipped so an env-var assignment is never returned
// as the "program". Splitting uses unicode.IsSpace, so tabs/newlines cannot
// defeat the strip the way a literal-space-only split would.
func CommandName(command string) string {
	fields := strings.FieldsFunc(command, unicode.IsSpace)
	i := 0
	for i < len(fields) && isEnvAssignment(fields[i]) {
		i++
	}
	if i >= len(fields) {
		return ""
	}
	name := fields[i]
	if len(name) > maxCommandNameLen {
		return name[:maxCommandNameLen]
	}
	return name
}

// isEnvAssignment reports whether s looks like a shell NAME=value token
// (^[A-Za-z_][A-Za-z0-9_]*=), including an empty value ("FOO=").
func isEnvAssignment(s string) bool {
	eq := strings.IndexByte(s, '=')
	if eq <= 0 {
		return false
	}
	name := s[:eq]
	if c := name[0]; c != '_' && (c < 'A' || (c > 'Z' && c < 'a') || c > 'z') {
		return false
	}
	for i := 1; i < len(name); i++ {
		c := name[i]
		if c == '_' || (c >= '0' && c <= '9') || (c >= 'A' && c <= 'Z') || (c >= 'a' && c <= 'z') {
			continue
		}
		return false
	}
	return true
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}
