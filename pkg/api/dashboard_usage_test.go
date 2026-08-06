package api

import (
	"testing"
	"time"

	"github.com/orion-belt-dev/orion-belt/pkg/common"
)

func TestBuildUsageMetrics(t *testing.T) {
	now := time.Date(2026, 7, 24, 12, 0, 0, 0, time.UTC)
	from := now.Add(-24 * time.Hour)

	review1 := now.Add(-10 * time.Hour)
	review2 := now.Add(-5 * time.Hour)

	sessions := []*common.Session{
		{ID: "s1", MachineID: "m1", StartTime: now.Add(-2 * time.Hour), Status: "active"},
		{ID: "s2", MachineID: "m1", StartTime: now.Add(-4 * time.Hour), Status: "completed"},
		{ID: "s3", MachineID: "m2", StartTime: now.Add(-3 * time.Hour), Status: "completed"},
		{ID: "s4", MachineID: "m3", StartTime: now.Add(-40 * time.Hour), Status: "completed"},
	}

	requests := []*common.AccessRequest{
		{ID: "r1", Status: "approved", RequestedAt: now.Add(-11 * time.Hour), ReviewedAt: &review1},
		{ID: "r2", Status: "rejected", RequestedAt: now.Add(-8 * time.Hour), ReviewedAt: &review2},
		{ID: "r3", Status: "pending", RequestedAt: now.Add(-2 * time.Hour)},
		{ID: "r4", Status: "approved", RequestedAt: now.Add(-30 * time.Hour), ReviewedAt: &review1},
	}

	volume, latency, top := buildUsageMetrics(from, now, 2, sessions, requests, map[string]string{
		"m1": "app-01",
		"m2": "db-01",
	})

	if volume.SessionsTotal != 3 {
		t.Fatalf("expected 3 sessions in window, got %d", volume.SessionsTotal)
	}
	if volume.SessionsActive != 1 {
		t.Fatalf("expected 1 active session in window, got %d", volume.SessionsActive)
	}
	if volume.RequestsTotal != 3 {
		t.Fatalf("expected 3 requests in window, got %d", volume.RequestsTotal)
	}
	if volume.RequestsApproved != 1 || volume.RequestsRejected != 1 || volume.RequestsPending != 1 {
		t.Fatalf("unexpected request status counts: %+v", volume)
	}

	if latency.SampleSize != 2 {
		t.Fatalf("expected 2 latency samples, got %d", latency.SampleSize)
	}
	if latency.AverageSeconds != 7200 {
		t.Fatalf("expected avg latency 7200s, got %.0f", latency.AverageSeconds)
	}
	if latency.P50Seconds != 7200 {
		t.Fatalf("expected p50 latency 7200s, got %.0f", latency.P50Seconds)
	}
	if latency.P95Seconds != 10440 {
		t.Fatalf("expected p95 latency 10440s, got %.0f", latency.P95Seconds)
	}

	if len(top) != 2 {
		t.Fatalf("expected top 2 targets, got %d", len(top))
	}
	if top[0].MachineID != "m1" || top[0].SessionCount != 2 {
		t.Fatalf("unexpected top[0]: %+v", top[0])
	}
	if top[1].MachineID != "m2" || top[1].SessionCount != 1 {
		t.Fatalf("unexpected top[1]: %+v", top[1])
	}
}

func TestPercentile(t *testing.T) {
	samples := []float64{10, 20, 30, 40, 50}
	if got := percentile(samples, 0); got != 10 {
		t.Fatalf("expected p0=10, got %.0f", got)
	}
	if got := percentile(samples, 0.5); got != 30 {
		t.Fatalf("expected p50=30, got %.0f", got)
	}
	if got := percentile(samples, 0.95); got != 48 {
		t.Fatalf("expected p95=48, got %.0f", got)
	}
	if got := percentile(samples, 1); got != 50 {
		t.Fatalf("expected p100=50, got %.0f", got)
	}
}
