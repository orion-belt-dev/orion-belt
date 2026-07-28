package metrics

import (
	"bufio"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// scrape renders the collector through its handler and returns the exposition
// text plus a metric-name -> value index of the sample lines.
func scrape(t *testing.T, c *Collector) (string, map[string]string) {
	t.Helper()

	rec := httptest.NewRecorder()
	c.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/metrics", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}

	body := rec.Body.String()
	samples := map[string]string{}
	sc := bufio.NewScanner(strings.NewReader(body))
	for sc.Scan() {
		line := sc.Text()
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		name, value, ok := strings.Cut(line, " ")
		if !ok {
			t.Errorf("malformed sample line %q", line)
			continue
		}
		if _, dup := samples[name]; dup {
			t.Errorf("metric %q exposed more than once", name)
		}
		samples[name] = value
	}
	if err := sc.Err(); err != nil {
		t.Fatalf("scanning body: %v", err)
	}
	return body, samples
}

func wantSample(t *testing.T, samples map[string]string, name, want string) {
	t.Helper()
	got, ok := samples[name]
	if !ok {
		t.Errorf("metric %q is missing from the exposition", name)
		return
	}
	if got != want {
		t.Errorf("%s = %s, want %s", name, got, want)
	}
}

func TestNewStartsAtZero(t *testing.T) {
	_, samples := scrape(t, New())

	for _, name := range []string{
		"orion_belt_ssh_sessions_total",
		"orion_belt_ssh_sessions_active",
		"orion_belt_api_requests_total",
		"orion_belt_auth_failures_total",
		"orion_belt_access_requests_total",
		"orion_belt_agents_connected",
	} {
		wantSample(t, samples, name, "0")
	}
}

func TestDefaultCollectorIsUsable(t *testing.T) {
	if Default == nil {
		t.Fatal("Default collector is nil")
	}
	// Exercised via the package-level singleton the server actually uses.
	before := Default.APIRequestsTotal.Load()
	Default.IncAPIRequest()
	if got := Default.APIRequestsTotal.Load(); got != before+1 {
		t.Errorf("Default.APIRequestsTotal = %d, want %d", got, before+1)
	}
}

func TestHandlerSetsPrometheusContentType(t *testing.T) {
	rec := httptest.NewRecorder()
	New().Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/metrics", nil))

	// Prometheus needs the version parameter to pick the text parser.
	if got, want := rec.Header().Get("Content-Type"), "text/plain; version=0.0.4; charset=utf-8"; got != want {
		t.Errorf("Content-Type = %q, want %q", got, want)
	}
}

// Every series needs its HELP and TYPE metadata, and the TYPE must match the
// semantics — a counter exposed as a gauge breaks rate() queries downstream.
func TestExpositionDeclaresHelpAndType(t *testing.T) {
	body, _ := scrape(t, New())

	wantTypes := map[string]string{
		"orion_belt_up":                    "gauge",
		"orion_belt_uptime_seconds":        "gauge",
		"orion_belt_ssh_sessions_total":    "counter",
		"orion_belt_ssh_sessions_active":   "gauge",
		"orion_belt_api_requests_total":    "counter",
		"orion_belt_auth_failures_total":   "counter",
		"orion_belt_access_requests_total": "counter",
		"orion_belt_agents_connected":      "gauge",
	}
	for name, typ := range wantTypes {
		if !strings.Contains(body, "# HELP "+name+" ") {
			t.Errorf("missing HELP line for %q", name)
		}
		if want := "# TYPE " + name + " " + typ; !strings.Contains(body, want) {
			t.Errorf("missing or wrong TYPE line for %q, want %q", name, want)
		}
	}
}

func TestUpIsAlwaysOne(t *testing.T) {
	_, samples := scrape(t, New())
	wantSample(t, samples, "orion_belt_up", "1")
}

func TestUptimeIsNonNegativeAndGrows(t *testing.T) {
	c := New()

	_, samples := scrape(t, c)
	raw, ok := samples["orion_belt_uptime_seconds"]
	if !ok {
		t.Fatal("orion_belt_uptime_seconds is missing")
	}
	// Rendered with %.0f, so it must parse as a plain integer-valued float.
	if _, err := strconv.ParseFloat(raw, 64); err != nil {
		t.Fatalf("uptime %q is not a number: %v", raw, err)
	}

	// Backdating the start time is the only way to observe growth without
	// making the test sleep for a second.
	c.startTime = time.Now().Add(-90 * time.Second)
	_, samples = scrape(t, c)
	if got := samples["orion_belt_uptime_seconds"]; got != "90" {
		t.Errorf("uptime after backdating 90s = %s, want 90", got)
	}
}

func TestCounterIncrements(t *testing.T) {
	cases := []struct {
		name   string
		metric string
		inc    func(*Collector)
		times  int
	}{
		{"api requests", "orion_belt_api_requests_total", (*Collector).IncAPIRequest, 3},
		{"auth failures", "orion_belt_auth_failures_total", (*Collector).IncAuthFailure, 2},
		{"access requests", "orion_belt_access_requests_total", (*Collector).IncAccessRequest, 5},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := New()
			for i := 0; i < tc.times; i++ {
				tc.inc(c)
			}
			_, samples := scrape(t, c)
			wantSample(t, samples, tc.metric, strconv.Itoa(tc.times))
		})
	}
}

// SessionStarted bumps both the lifetime counter and the active gauge, but
// SessionEnded must only decrement the gauge — decrementing the counter too
// would make the total non-monotonic.
func TestSessionLifecycleTracksTotalAndActiveSeparately(t *testing.T) {
	c := New()

	c.SessionStarted()
	c.SessionStarted()
	c.SessionStarted()

	_, samples := scrape(t, c)
	wantSample(t, samples, "orion_belt_ssh_sessions_total", "3")
	wantSample(t, samples, "orion_belt_ssh_sessions_active", "3")

	c.SessionEnded()
	c.SessionEnded()

	_, samples = scrape(t, c)
	wantSample(t, samples, "orion_belt_ssh_sessions_total", "3") // unchanged
	wantSample(t, samples, "orion_belt_ssh_sessions_active", "1")
}

// The active gauge is signed, so an unbalanced SessionEnded shows up as a
// negative value rather than wrapping to a huge number.
func TestUnbalancedSessionEndedGoesNegative(t *testing.T) {
	c := New()
	c.SessionEnded()

	_, samples := scrape(t, c)
	wantSample(t, samples, "orion_belt_ssh_sessions_active", "-1")
}

func TestSetAgentsConnectedReplacesRatherThanAccumulates(t *testing.T) {
	c := New()

	c.SetAgentsConnected(7)
	_, samples := scrape(t, c)
	wantSample(t, samples, "orion_belt_agents_connected", "7")

	c.SetAgentsConnected(2)
	_, samples = scrape(t, c)
	wantSample(t, samples, "orion_belt_agents_connected", "2")

	c.SetAgentsConnected(0)
	_, samples = scrape(t, c)
	wantSample(t, samples, "orion_belt_agents_connected", "0")
}

// The collector is process-wide and written from every request goroutine, so
// the atomics have to hold up under -race.
func TestConcurrentUpdatesAreRaceFree(t *testing.T) {
	c := New()

	const goroutines, perGoroutine = 16, 100
	var wg sync.WaitGroup
	wg.Add(goroutines)
	for i := 0; i < goroutines; i++ {
		go func() {
			defer wg.Done()
			for j := 0; j < perGoroutine; j++ {
				c.IncAPIRequest()
				c.IncAuthFailure()
				c.IncAccessRequest()
				c.SessionStarted()
				c.SessionEnded()
			}
		}()
	}
	wg.Wait()

	want := strconv.Itoa(goroutines * perGoroutine)
	_, samples := scrape(t, c)
	wantSample(t, samples, "orion_belt_api_requests_total", want)
	wantSample(t, samples, "orion_belt_auth_failures_total", want)
	wantSample(t, samples, "orion_belt_access_requests_total", want)
	wantSample(t, samples, "orion_belt_ssh_sessions_total", want)
	wantSample(t, samples, "orion_belt_ssh_sessions_active", "0") // balanced
}

// Scraping concurrently with updates is the normal production pattern.
func TestHandlerIsSafeDuringConcurrentUpdates(t *testing.T) {
	c := New()
	done := make(chan struct{})

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			select {
			case <-done:
				return
			default:
				c.IncAPIRequest()
			}
		}
	}()

	for i := 0; i < 50; i++ {
		rec := httptest.NewRecorder()
		c.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/metrics", nil))
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d during concurrent updates, want 200", rec.Code)
		}
	}

	close(done)
	wg.Wait()
}
