package tracing

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"

	"github.com/orion-belt-dev/orion-belt/pkg/common"
)

// initForTest runs Init with cfg and shuts the provider down at cleanup. The
// endpoints used are TEST-NET addresses: exporters connect lazily, so nothing
// is ever dialled.
func initForTest(t *testing.T, cfg Config, logger *common.Logger) {
	t.Helper()
	withTracingDisabled(t)
	shutdown, err := Init(context.Background(), cfg, logger)
	if err != nil {
		t.Fatalf("Init(%+v): %v", cfg, err)
	}
	t.Cleanup(func() { _ = shutdown(context.Background()) })
}

func TestInit_ExporterVariants_EnableTracing(t *testing.T) {
	cases := []struct {
		name string
		cfg  Config
	}{
		{"grpc with endpoint, headers and version", Config{
			Protocol: ProtocolGRPC, Endpoint: "192.0.2.1:4317", Insecure: true,
			Headers: map[string]string{"x-api-key": "k"}, ServiceVersion: "1.2.3",
		}},
		{"grpc from environment", Config{Protocol: ProtocolGRPC}},
		{"http from environment with headers", Config{
			Protocol: ProtocolHTTP, Headers: map[string]string{"x-api-key": "k"},
		}},
		{"https endpoint", Config{Protocol: ProtocolHTTP, Endpoint: "192.0.2.1:4318"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tc.cfg.Enabled = true
			tc.cfg.ServiceName = "svc"
			initForTest(t, tc.cfg, nil)

			if !enabled.Load() {
				t.Fatal("tracing not enabled after a successful Init")
			}
			// The span is deliberately never ended: an ended span would be
			// flushed at shutdown to a collector that never answers, costing
			// the full export timeout.
			_, span := Start(context.Background(), "op")
			if !span.IsRecording() {
				t.Error("span from an initialised provider is not recording")
			}
		})
	}
}

func TestInit_WithLogger_LogsStartupAndRoutesExporterErrors(t *testing.T) {
	prevHandler := otel.GetErrorHandler()
	t.Cleanup(func() { otel.SetErrorHandler(prevHandler) })

	var logs bytes.Buffer
	initForTest(t, Config{Enabled: true, ServiceName: "svc", Protocol: ProtocolGRPC}, common.NewLoggerTo(common.DEBUG, &logs))

	if !strings.Contains(logs.String(), "Tracing enabled: service=svc protocol=grpc endpoint=(from OTEL_EXPORTER_OTLP_* environment)") {
		t.Errorf("startup log = %q, want service, protocol and env-endpoint note", logs.String())
	}

	otel.Handle(errors.New("collector unreachable"))
	if !strings.Contains(logs.String(), "tracing exporter error: collector unreachable") {
		t.Errorf("log = %q, want the exporter error routed to the logger", logs.String())
	}
}

func TestShutdown_IsIdempotentAndAllowsReinit(t *testing.T) {
	withTracingDisabled(t)
	cfg := Config{Enabled: true, ServiceName: "svc", Protocol: ProtocolHTTP, Endpoint: "192.0.2.1:4318", Insecure: true}

	shutdown, err := Init(context.Background(), cfg, nil)
	if err != nil {
		t.Fatalf("Init: %v", err)
	}
	if err := shutdown(context.Background()); err != nil {
		t.Fatalf("shutdown: %v", err)
	}
	if err := shutdown(context.Background()); err != nil {
		t.Errorf("second shutdown = %v, want the first result again", err)
	}
	if enabled.Load() {
		t.Error("tracing still enabled after shutdown")
	}

	again, err := Init(context.Background(), cfg, nil)
	if err != nil {
		t.Fatalf("Init after shutdown: %v", err)
	}
	_ = again(context.Background())
}

func TestNewExporter_UnknownProtocol_ReturnsError(t *testing.T) {
	_, err := newExporter(context.Background(), Config{Protocol: "carrier-pigeon"})
	if err == nil || !strings.Contains(err.Error(), `unknown protocol "carrier-pigeon"`) {
		t.Fatalf("newExporter error = %v, want unknown-protocol error", err)
	}
}

func TestEndpointForLog(t *testing.T) {
	if got := endpointForLog(Config{Endpoint: "collector:4317"}); got != "collector:4317" {
		t.Errorf("endpointForLog = %q, want the configured endpoint", got)
	}
	if got := endpointForLog(Config{}); !strings.Contains(got, "environment") {
		t.Errorf("endpointForLog(empty) = %q, want a note about the environment", got)
	}
}

func TestNewResource_IncludesServiceVersionWhenSet(t *testing.T) {
	has := func(cfg Config, key attribute.Key) bool {
		_, ok := newResource(cfg).Set().Value(key)
		return ok
	}
	if !has(Config{ServiceName: "svc", ServiceVersion: "1.0"}, "service.version") {
		t.Error("resource lacks service.version when one is configured")
	}
	if has(Config{ServiceName: "svc"}, "service.version") {
		t.Error("resource has service.version when none is configured")
	}
}

func TestSetAttributesAndRecordError_OnRecordingSpan(t *testing.T) {
	recorder := withRecordingTracer(t)

	_, span := Start(context.Background(), "op")
	SetAttributes(span, attribute.String("user", "alice"))
	RecordError(span, errors.New("boom"))
	span.End()

	got := recorder.Ended()[0]
	if v, ok := attrValue(got.Attributes(), "user"); !ok || v != "alice" {
		t.Errorf("span attribute user = %q (present=%v), want alice", v, ok)
	}
	if got.Status().Code != codes.Error || got.Status().Description != "boom" {
		t.Errorf("span status = %+v, want Error/boom", got.Status())
	}
	if len(got.Events()) != 1 || got.Events()[0].Name != "exception" {
		t.Errorf("span events = %+v, want one exception event", got.Events())
	}
}

func attrValue(attrs []attribute.KeyValue, key attribute.Key) (string, bool) {
	for _, a := range attrs {
		if a.Key == key {
			return a.Value.AsString(), true
		}
	}
	return "", false
}

func TestIsEnvAssignment_RejectsInvalidNameCharacters(t *testing.T) {
	for _, s := range []string{"A-B=1", "FOO.BAR=x", "x y=1"} {
		if isEnvAssignment(s) {
			t.Errorf("isEnvAssignment(%q) = true, want false", s)
		}
	}
	if got := CommandName("A-B=1 ls"); got != "A-B=1" {
		t.Errorf("CommandName = %q, want the invalid assignment treated as the program", got)
	}
}

func TestFirstNonEmpty(t *testing.T) {
	if got := firstNonEmpty("", "  ", "b", "c"); got != "b" {
		t.Errorf("firstNonEmpty = %q, want b", got)
	}
	if got := firstNonEmpty("", " \t"); got != "" {
		t.Errorf("firstNonEmpty(blank) = %q, want empty", got)
	}
}
