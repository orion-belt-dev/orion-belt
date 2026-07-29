package tracing

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"go.opentelemetry.io/otel/trace"
	collectortrace "go.opentelemetry.io/proto/otlp/collector/trace/v1"
	"google.golang.org/protobuf/proto"
)

// AC2, verified against a real OTLP receiver rather than by inspection: run
// Init for real, emit a gateway span and a linked agent span, and assert that
// what arrives on the wire is a well-formed OTLP request containing both, in
// one trace, with the service name attached.
//
// This exercises the actual exporter, batcher, and shutdown flush — the parts
// that a mocked tracer would skip and that are the usual cause of "tracing is
// on but nothing shows up in the collector".
func TestOTLPExportDeliversLinkedSpans(t *testing.T) {
	withTracingDisabled(t) // ensure a clean starting state; Init flips it on

	type received struct {
		contentType string
		body        []byte
	}
	// Buffered generously: the handler must never block, or a retry storm
	// would deadlock the flush instead of failing the assertion below.
	requests := make(chan received, 64)

	collector := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/traces" {
			// The OTLP/HTTP spec fixes this path; a change here would mean
			// spans silently 404 against a standard collector.
			t.Errorf("unexpected OTLP path %q", r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
			return
		}
		body, _ := io.ReadAll(r.Body)
		requests <- received{contentType: r.Header.Get("Content-Type"), body: body}

		// Minimal valid OTLP success response.
		resp, _ := proto.Marshal(&collectortrace.ExportTraceServiceResponse{})
		w.Header().Set("Content-Type", "application/x-protobuf")
		w.WriteHeader(http.StatusOK)
		w.Write(resp)
	}))
	defer collector.Close()

	shutdown, err := Init(context.Background(), Config{
		Enabled:     true,
		Endpoint:    collector.URL, // full URL exercises the URL-passthrough branch
		Protocol:    ProtocolHTTP,
		Insecure:    true,
		ServiceName: "orion-belt-gateway",
		SampleRatio: 1.0,
	}, nil)
	if err != nil {
		t.Fatalf("Init: %v", err)
	}
	t.Cleanup(func() { enabled.Store(false) })

	if !Enabled() {
		t.Fatal("Init should have enabled tracing")
	}

	// Emit the shape this feature exists to produce: a gateway span, its
	// context carried across the SSH hop, and an agent span continuing it.
	gatewayCtx, gatewaySpan := Start(context.Background(), "gateway.ssh.session",
		trace.WithSpanKind(trace.SpanKindServer))
	openCtx, openSpan := Start(gatewayCtx, "gateway.agent.open_channel",
		trace.WithSpanKind(trace.SpanKindClient))

	agentCtx := ExtractChannelData(context.Background(), InjectChannelData(openCtx))
	_, agentSpan := Start(agentCtx, "agent.session", trace.WithSpanKind(trace.SpanKindServer))
	agentSpan.End()
	openSpan.End()
	gatewaySpan.End()

	// Shutdown flushes the batcher — this is also the assertion that the
	// shutdown path actually drains rather than dropping buffered spans.
	flushCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := shutdown(flushCtx); err != nil {
		t.Fatalf("shutdown/flush: %v", err)
	}

	if Enabled() {
		t.Error("shutdown must leave tracing disabled")
	}

	close(requests)
	names := map[string]bool{}
	traceIDs := map[string]bool{}
	var sawService bool
	var gotAny bool

	for req := range requests {
		gotAny = true
		if !strings.Contains(req.contentType, "protobuf") {
			t.Errorf("expected a protobuf content type, got %q", req.contentType)
		}

		var export collectortrace.ExportTraceServiceRequest
		if err := proto.Unmarshal(req.body, &export); err != nil {
			t.Fatalf("collector received a body that is not valid OTLP: %v", err)
		}

		for _, rs := range export.GetResourceSpans() {
			for _, attr := range rs.GetResource().GetAttributes() {
				if attr.GetKey() == "service.name" && attr.GetValue().GetStringValue() == "orion-belt-gateway" {
					sawService = true
				}
			}
			for _, ss := range rs.GetScopeSpans() {
				for _, span := range ss.GetSpans() {
					names[span.GetName()] = true
					traceIDs[string(span.GetTraceId())] = true
				}
			}
		}
	}

	if !gotAny {
		t.Fatal("collector received no OTLP requests at all")
	}
	for _, want := range []string{"gateway.ssh.session", "gateway.agent.open_channel", "agent.session"} {
		if !names[want] {
			t.Errorf("span %q never reached the collector (got %v)", want, keys(names))
		}
	}
	if !sawService {
		t.Error("service.name was not attached to the exported resource")
	}
	// AC1 end-to-end: the agent's span must not have landed in its own trace.
	if len(traceIDs) != 1 {
		t.Errorf("expected all spans in one trace, got %d distinct trace IDs", len(traceIDs))
	}
}

// A collector that is unreachable must degrade to "no traces", never to a
// broken gateway: startup still succeeds and spans are still safe to create.
func TestExportSurvivesUnreachableCollector(t *testing.T) {
	withTracingDisabled(t)

	shutdown, err := Init(context.Background(), Config{
		Enabled: true,
		// Reserved TEST-NET-1 address; nothing will answer.
		Endpoint:    "192.0.2.1:4318",
		Protocol:    ProtocolHTTP,
		Insecure:    true,
		ServiceName: "orion-belt-gateway",
	}, nil)
	if err != nil {
		t.Fatalf("Init should not fail merely because the collector is down: %v", err)
	}
	t.Cleanup(func() { enabled.Store(false) })

	_, span := Start(context.Background(), "gateway.ssh.session")
	span.End()

	// Shutdown may report the export failure, but it must return promptly
	// rather than hanging a restart on a dead collector.
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	done := make(chan struct{})
	go func() {
		_ = shutdown(ctx)
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(15 * time.Second):
		t.Fatal("shutdown hung on an unreachable collector")
	}
}

func keys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
