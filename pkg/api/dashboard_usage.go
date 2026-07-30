package api

import (
	"net/http"
	"sort"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/orion-belt-dev/orion-belt/pkg/common"
)

type usageDashboardResponse struct {
	WindowHours int                        `json:"window_hours"`
	From        string                     `json:"from"`
	To          string                     `json:"to"`
	GeneratedAt string                     `json:"generated_at"`
	Volume      usageVolumeMetrics         `json:"access_volume"`
	Latency     usageApprovalLatencyMetric `json:"approval_latency"`
	TopTargets  []usageTopTarget           `json:"top_targets"`
}

type usageVolumeMetrics struct {
	SessionsTotal    int `json:"sessions_total"`
	SessionsActive   int `json:"sessions_active"`
	RequestsTotal    int `json:"requests_total"`
	RequestsPending  int `json:"requests_pending"`
	RequestsApproved int `json:"requests_approved"`
	RequestsRejected int `json:"requests_rejected"`
}

type usageApprovalLatencyMetric struct {
	SampleSize     int     `json:"sample_size"`
	AverageSeconds float64 `json:"average_seconds"`
	P50Seconds     float64 `json:"p50_seconds"`
	P95Seconds     float64 `json:"p95_seconds"`
}

type usageTopTarget struct {
	MachineID    string `json:"machine_id"`
	MachineName  string `json:"machine_name"`
	SessionCount int    `json:"session_count"`
}

func (s *APIServer) usageDashboard(c *gin.Context) {
	if !isPrivilegedViewer(c) {
		c.JSON(http.StatusForbidden, gin.H{"error": "admin, operator, or auditor privileges required"})
		return
	}

	ctx := c.Request.Context()
	now := time.Now().UTC()
	windowHours := parseLimit(c.Query("window_hours"), 24, 24*30)
	topN := parseLimit(c.Query("top"), 5, 20)
	from := now.Add(-time.Duration(windowHours) * time.Hour)

	sessions, err := s.store.ListSessions(ctx, 10000, 0)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	requests, err := s.store.ListAllAccessRequests(ctx, 10000, 0)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	machines, _ := s.store.ListMachines(ctx, 5000, 0)
	machineNames := make(map[string]string, len(machines))
	for _, m := range machines {
		machineNames[m.ID] = m.Name
	}

	volume, latency, top := buildUsageMetrics(from, now, topN, sessions, requests, machineNames)

	c.JSON(http.StatusOK, usageDashboardResponse{
		WindowHours: windowHours,
		From:        from.Format(time.RFC3339),
		To:          now.Format(time.RFC3339),
		GeneratedAt: now.Format(time.RFC3339),
		Volume:      volume,
		Latency:     latency,
		TopTargets:  top,
	})
}

func buildUsageMetrics(from, to time.Time, topN int, sessions []*common.Session, requests []*common.AccessRequest, machineNames map[string]string) (usageVolumeMetrics, usageApprovalLatencyMetric, []usageTopTarget) {
	volume := usageVolumeMetrics{}
	machineCounts := map[string]int{}
	latencySamples := make([]float64, 0, len(requests))

	for _, sess := range sessions {
		if sess == nil {
			continue
		}
		start := sess.StartTime.UTC()
		if start.Before(from) || start.After(to) {
			continue
		}
		volume.SessionsTotal++
		if sess.Status == "active" {
			volume.SessionsActive++
		}
		machineCounts[sess.MachineID]++
	}

	for _, req := range requests {
		if req == nil {
			continue
		}
		requestedAt := req.RequestedAt.UTC()
		if requestedAt.Before(from) || requestedAt.After(to) {
			continue
		}
		volume.RequestsTotal++
		switch req.Status {
		case "pending":
			volume.RequestsPending++
		case "approved":
			volume.RequestsApproved++
		case "rejected":
			volume.RequestsRejected++
		}
		if req.ReviewedAt != nil {
			reviewed := req.ReviewedAt.UTC()
			if reviewed.After(requestedAt) {
				latencySamples = append(latencySamples, reviewed.Sub(requestedAt).Seconds())
			}
		}
	}

	latency := summarizeLatency(latencySamples)
	topTargets := rankTopTargets(machineCounts, machineNames, topN)

	return volume, latency, topTargets
}

func summarizeLatency(samples []float64) usageApprovalLatencyMetric {
	if len(samples) == 0 {
		return usageApprovalLatencyMetric{}
	}

	sort.Float64s(samples)
	sum := 0.0
	for _, s := range samples {
		sum += s
	}

	return usageApprovalLatencyMetric{
		SampleSize:     len(samples),
		AverageSeconds: sum / float64(len(samples)),
		P50Seconds:     percentile(samples, 0.50),
		P95Seconds:     percentile(samples, 0.95),
	}
}

func percentile(sorted []float64, q float64) float64 {
	if len(sorted) == 0 {
		return 0
	}
	if q <= 0 {
		return sorted[0]
	}
	if q >= 1 {
		return sorted[len(sorted)-1]
	}
	if len(sorted) == 1 {
		return sorted[0]
	}
	pos := q * float64(len(sorted)-1)
	low := int(pos)
	high := low + 1
	if high >= len(sorted) {
		return sorted[len(sorted)-1]
	}
	weight := pos - float64(low)
	return sorted[low] + (sorted[high]-sorted[low])*weight
}

func rankTopTargets(machineCounts map[string]int, machineNames map[string]string, topN int) []usageTopTarget {
	top := make([]usageTopTarget, 0, len(machineCounts))
	for machineID, count := range machineCounts {
		name := machineNames[machineID]
		if name == "" {
			name = machineID
		}
		top = append(top, usageTopTarget{
			MachineID:    machineID,
			MachineName:  name,
			SessionCount: count,
		})
	}

	sort.Slice(top, func(i, j int) bool {
		if top[i].SessionCount == top[j].SessionCount {
			return top[i].MachineName < top[j].MachineName
		}
		return top[i].SessionCount > top[j].SessionCount
	})

	if topN <= 0 || len(top) <= topN {
		return top
	}
	return top[:topN]
}
