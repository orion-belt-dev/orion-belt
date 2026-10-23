package common

import (
	"errors"
	"fmt"
	"net"
	"slices"
	"strings"
	"time"
)

// Outcomes of evaluating an access request against the risk policy.
const (
	RiskOutcomeManual      = "manual"       // the usual review
	RiskOutcomeAutoApprove = "auto_approve" // approved by policy, no reviewer
	RiskOutcomeEscalate    = "escalate"     // only an admin may approve
)

// maxAutoApproveDuration caps what a policy may approve unattended.
const maxAutoApproveDuration = 24 * 60 * 60

// HourRange is a window of UTC hours, Start inclusive and End exclusive. It
// may wrap midnight: {22, 6} is 22:00-06:00.
type HourRange struct {
	Start int `json:"start"`
	End   int `json:"end"`
}

// Contains reports whether hour falls inside the range.
func (r HourRange) Contains(hour int) bool {
	if r.Start <= r.End {
		return hour >= r.Start && hour < r.End
	}
	return hour >= r.Start || hour < r.End
}

func (r HourRange) validate() error {
	if r.Start < 0 || r.Start > 23 || r.End < 0 || r.End > 24 || r.Start == r.End {
		return fmt.Errorf("hours %d-%d: start must be 0-23, end 0-24, and they must differ", r.Start, r.End)
	}
	return nil
}

// AutoApproveRule approves a request without a reviewer only when every
// condition it sets holds. MaxSensitivity and MaxDurationSeconds are
// mandatory, and high-sensitivity targets can never be auto-approved.
type AutoApproveRule struct {
	Enabled            bool       `json:"enabled"`
	MaxSensitivity     string     `json:"max_sensitivity"` // low | medium
	MaxDurationSeconds int        `json:"max_duration_seconds"`
	HoursUTC           *HourRange `json:"hours_utc,omitempty"`
	SourceCIDRs        []string   `json:"source_cidrs,omitempty"`
	RequireMFA         bool       `json:"require_mfa"`
	// RemoteUsers, when set, lists the only accounts that may be
	// auto-approved (e.g. ["deploy"] keeps root out).
	RemoteUsers []string `json:"remote_users,omitempty"`
}

// EscalateRule sends a request to an admin when any condition it sets
// matches. Escalation wins over auto-approval.
type EscalateRule struct {
	Enabled            bool       `json:"enabled"`
	Sensitivities      []string   `json:"sensitivities,omitempty"`
	OutsideHoursUTC    *HourRange `json:"outside_hours_utc,omitempty"`
	WithoutMFA         bool       `json:"without_mfa"`
	SourceOutsideCIDRs []string   `json:"source_outside_cidrs,omitempty"`
}

// RiskPolicy is the admin-set rule for routing access requests.
type RiskPolicy struct {
	AutoApprove AutoApproveRule `json:"auto_approve"`
	Escalate    EscalateRule    `json:"escalate"`
	UpdatedAt   time.Time       `json:"updated_at"`
	UpdatedBy   string          `json:"updated_by,omitempty"`
}

// RiskDecision is how a request was routed, and why.
type RiskDecision struct {
	Outcome string   `json:"outcome"`
	Reasons []string `json:"reasons"`
}

// Validate rejects a policy that could approve more than intended.
func (p *RiskPolicy) Validate() error {
	var errs []error
	a := p.AutoApprove
	if a.Enabled {
		if a.MaxSensitivity != SensitivityLow && a.MaxSensitivity != SensitivityMedium {
			errs = append(errs, errors.New("auto_approve.max_sensitivity must be low or medium"))
		}
		if a.MaxDurationSeconds <= 0 || a.MaxDurationSeconds > maxAutoApproveDuration {
			errs = append(errs, fmt.Errorf("auto_approve.max_duration_seconds must be 1-%d", maxAutoApproveDuration))
		}
		if a.HoursUTC != nil {
			if err := a.HoursUTC.validate(); err != nil {
				errs = append(errs, fmt.Errorf("auto_approve.hours_utc: %w", err))
			}
		}
		errs = append(errs, validateCIDRs("auto_approve.source_cidrs", a.SourceCIDRs)...)
	}
	e := p.Escalate
	if e.Enabled {
		for _, s := range e.Sensitivities {
			if !slices.Contains([]string{SensitivityLow, SensitivityMedium, SensitivityHigh, SensitivityUnset}, s) {
				errs = append(errs, fmt.Errorf("escalate.sensitivities: unknown level %q", s))
			}
		}
		if e.OutsideHoursUTC != nil {
			if err := e.OutsideHoursUTC.validate(); err != nil {
				errs = append(errs, fmt.Errorf("escalate.outside_hours_utc: %w", err))
			}
		}
		errs = append(errs, validateCIDRs("escalate.source_outside_cidrs", e.SourceOutsideCIDRs)...)
	}
	return errors.Join(errs...)
}

func validateCIDRs(field string, cidrs []string) []error {
	var errs []error
	for _, c := range cidrs {
		if _, _, err := net.ParseCIDR(c); err != nil {
			errs = append(errs, fmt.Errorf("%s: %q is not a CIDR", field, c))
		}
	}
	return errs
}

func ipIn(ip string, cidrs []string) bool {
	addr := net.ParseIP(ip)
	if addr == nil {
		return false
	}
	for _, c := range cidrs {
		if _, n, err := net.ParseCIDR(c); err == nil && n.Contains(addr) {
			return true
		}
	}
	return false
}

var sensitivityRank = map[string]int{SensitivityLow: 1, SensitivityMedium: 2, SensitivityHigh: 3}

// EvaluateRisk routes a request. A nil policy, or nil signals, means manual
// review. Escalation is checked first; auto-approval needs every one of its
// conditions to hold, and the reasons say which held or which failed.
func EvaluateRisk(p *RiskPolicy, s *RiskSignals, remoteUsers []string, durationSeconds int) RiskDecision {
	if p == nil || s == nil {
		return RiskDecision{Outcome: RiskOutcomeManual, Reasons: []string{"no risk policy"}}
	}

	if e := p.Escalate; e.Enabled {
		var why []string
		if slices.Contains(e.Sensitivities, s.TargetSensitivity) {
			why = append(why, "target sensitivity is "+s.TargetSensitivity)
		}
		if e.OutsideHoursUTC != nil && !e.OutsideHoursUTC.Contains(s.HourUTC) {
			why = append(why, fmt.Sprintf("requested at %02d:00 UTC, outside %02d-%02d", s.HourUTC, e.OutsideHoursUTC.Start, e.OutsideHoursUTC.End))
		}
		if e.WithoutMFA && !s.MFAEnrolled {
			why = append(why, "requester has no MFA enrolled")
		}
		if len(e.SourceOutsideCIDRs) > 0 && !ipIn(s.SourceIP, e.SourceOutsideCIDRs) {
			why = append(why, "source "+orUnknown(s.SourceIP)+" is outside the trusted networks")
		}
		if len(why) > 0 {
			return RiskDecision{Outcome: RiskOutcomeEscalate, Reasons: why}
		}
	}

	a := p.AutoApprove
	if !a.Enabled {
		return RiskDecision{Outcome: RiskOutcomeManual, Reasons: []string{"auto-approval is off"}}
	}
	var failed []string
	if rank, ok := sensitivityRank[s.TargetSensitivity]; !ok || rank > sensitivityRank[a.MaxSensitivity] {
		failed = append(failed, "target sensitivity "+s.TargetSensitivity+" is above "+a.MaxSensitivity)
	}
	if durationSeconds <= 0 || durationSeconds > a.MaxDurationSeconds {
		failed = append(failed, fmt.Sprintf("duration %ds is not within 1-%ds", durationSeconds, a.MaxDurationSeconds))
	}
	if a.HoursUTC != nil && !a.HoursUTC.Contains(s.HourUTC) {
		failed = append(failed, fmt.Sprintf("requested at %02d:00 UTC, outside %02d-%02d", s.HourUTC, a.HoursUTC.Start, a.HoursUTC.End))
	}
	if len(a.SourceCIDRs) > 0 && !ipIn(s.SourceIP, a.SourceCIDRs) {
		failed = append(failed, "source "+orUnknown(s.SourceIP)+" is not in an allowed network")
	}
	if a.RequireMFA && !s.MFAEnrolled {
		failed = append(failed, "requester has no MFA enrolled")
	}
	if len(a.RemoteUsers) > 0 {
		for _, u := range remoteUsers {
			if !slices.Contains(a.RemoteUsers, u) {
				failed = append(failed, "account "+u+" is not auto-approvable")
			}
		}
	}
	if len(failed) > 0 {
		return RiskDecision{Outcome: RiskOutcomeManual, Reasons: failed}
	}
	return RiskDecision{Outcome: RiskOutcomeAutoApprove, Reasons: []string{
		fmt.Sprintf("low risk: %s-sensitivity target, %ds, from %s", s.TargetSensitivity, durationSeconds, orUnknown(s.SourceIP)),
	}}
}

func orUnknown(v string) string {
	if strings.TrimSpace(v) == "" {
		return "an unknown source"
	}
	return v
}
