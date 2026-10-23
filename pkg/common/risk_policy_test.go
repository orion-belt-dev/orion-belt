package common

import (
	"strings"
	"testing"
)

func basePolicy() *RiskPolicy {
	return &RiskPolicy{
		AutoApprove: AutoApproveRule{
			Enabled: true, MaxSensitivity: SensitivityLow, MaxDurationSeconds: 3600,
			HoursUTC: &HourRange{Start: 8, End: 18}, SourceCIDRs: []string{"10.0.0.0/8"},
			RequireMFA: true, RemoteUsers: []string{"deploy"},
		},
		Escalate: EscalateRule{Enabled: true, Sensitivities: []string{SensitivityHigh}, WithoutMFA: true},
	}
}

func lowRiskSignals() *RiskSignals {
	return &RiskSignals{SourceIP: "10.1.2.3", MFAEnrolled: true, HourUTC: 10, TargetSensitivity: SensitivityLow}
}

func TestEvaluateRiskAutoApprovesOnlyWhenEveryConditionHolds(t *testing.T) {
	if d := EvaluateRisk(basePolicy(), lowRiskSignals(), []string{"deploy"}, 1800); d.Outcome != RiskOutcomeAutoApprove {
		t.Fatalf("decision = %+v", d)
	}

	cases := map[string]struct {
		mutate   func(*RiskSignals)
		accounts []string
		duration int
		reason   string
	}{
		"medium target":    {func(s *RiskSignals) { s.TargetSensitivity = SensitivityMedium }, []string{"deploy"}, 1800, "above low"},
		"unset target":     {func(s *RiskSignals) { s.TargetSensitivity = SensitivityUnset }, []string{"deploy"}, 1800, "above low"},
		"too long":         {func(*RiskSignals) {}, []string{"deploy"}, 7200, "duration"},
		"unlimited":        {func(*RiskSignals) {}, []string{"deploy"}, 0, "duration"},
		"after hours":      {func(s *RiskSignals) { s.HourUTC = 18 }, []string{"deploy"}, 1800, "outside 08-18"},
		"outside network":  {func(s *RiskSignals) { s.SourceIP = "192.0.2.1" }, []string{"deploy"}, 1800, "not in an allowed network"},
		"root not allowed": {func(*RiskSignals) {}, []string{"deploy", "root"}, 1800, "account root"},
	}
	for name, c := range cases {
		s := lowRiskSignals()
		c.mutate(s)
		d := EvaluateRisk(basePolicy(), s, c.accounts, c.duration)
		if d.Outcome != RiskOutcomeManual || !strings.Contains(strings.Join(d.Reasons, "; "), c.reason) {
			t.Errorf("%s: decision = %+v, want manual mentioning %q", name, d, c.reason)
		}
	}
}

// With escalation off, a requester without MFA is held for review rather
// than auto-approved.
func TestEvaluateRiskAutoApproveRequiresMFA(t *testing.T) {
	p := basePolicy()
	p.Escalate.Enabled = false
	s := lowRiskSignals()
	s.MFAEnrolled = false
	d := EvaluateRisk(p, s, []string{"deploy"}, 1800)
	if d.Outcome != RiskOutcomeManual || !strings.Contains(strings.Join(d.Reasons, ";"), "no MFA") {
		t.Errorf("decision = %+v", d)
	}
}

func TestEvaluateRiskEscalationWins(t *testing.T) {
	s := lowRiskSignals()
	s.MFAEnrolled = false
	d := EvaluateRisk(basePolicy(), s, []string{"deploy"}, 1800)
	if d.Outcome != RiskOutcomeEscalate || d.Reasons[0] != "requester has no MFA enrolled" {
		t.Fatalf("decision = %+v", d)
	}
	s = lowRiskSignals()
	s.TargetSensitivity = SensitivityHigh
	if d := EvaluateRisk(basePolicy(), s, []string{"deploy"}, 1800); d.Outcome != RiskOutcomeEscalate {
		t.Errorf("high target: %+v", d)
	}
	p := basePolicy()
	p.Escalate = EscalateRule{Enabled: true, OutsideHoursUTC: &HourRange{Start: 22, End: 6}, SourceOutsideCIDRs: []string{"10.0.0.0/8"}}
	s = lowRiskSignals() // 10:00 is outside a 22-06 window
	if d := EvaluateRisk(p, s, []string{"deploy"}, 1800); d.Outcome != RiskOutcomeEscalate || len(d.Reasons) != 1 {
		t.Errorf("hours escalation: %+v", d)
	}
}

func TestEvaluateRiskDefaultsToManual(t *testing.T) {
	for name, d := range map[string]RiskDecision{
		"no policy":  EvaluateRisk(nil, lowRiskSignals(), nil, 60),
		"no signals": EvaluateRisk(basePolicy(), nil, nil, 60),
		"rules off":  EvaluateRisk(&RiskPolicy{}, lowRiskSignals(), nil, 60),
	} {
		if d.Outcome != RiskOutcomeManual {
			t.Errorf("%s: %+v", name, d)
		}
	}
}

func TestRiskPolicyValidate(t *testing.T) {
	if err := basePolicy().Validate(); err != nil {
		t.Fatalf("valid policy rejected: %v", err)
	}
	bad := basePolicy()
	bad.AutoApprove.MaxSensitivity = SensitivityHigh
	bad.AutoApprove.MaxDurationSeconds = 0
	bad.AutoApprove.SourceCIDRs = []string{"10.0.0.1"}
	bad.AutoApprove.HoursUTC = &HourRange{Start: 9, End: 9}
	bad.Escalate.Sensitivities = []string{"critical"}
	bad.Escalate.OutsideHoursUTC = &HourRange{Start: 25, End: 3}
	err := bad.Validate()
	if err == nil {
		t.Fatal("invalid policy accepted")
	}
	for _, want := range []string{"max_sensitivity", "max_duration_seconds", "not a CIDR", "must differ", "critical", "escalate.outside_hours_utc"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not mention %q", err, want)
		}
	}
	if err := (&RiskPolicy{}).Validate(); err != nil {
		t.Errorf("an all-off policy is valid: %v", err)
	}
}

func TestHourRangeWrapsMidnight(t *testing.T) {
	r := HourRange{Start: 22, End: 6}
	for h, want := range map[int]bool{22: true, 23: true, 0: true, 5: true, 6: false, 12: false} {
		if r.Contains(h) != want {
			t.Errorf("%d: got %v", h, !want)
		}
	}
}

func TestEvaluateRiskNamesAnUnknownSource(t *testing.T) {
	p := &RiskPolicy{Escalate: EscalateRule{Enabled: true, SourceOutsideCIDRs: []string{"10.0.0.0/8"}}}
	d := EvaluateRisk(p, &RiskSignals{TargetSensitivity: SensitivityLow}, nil, 60)
	if d.Outcome != RiskOutcomeEscalate || !strings.Contains(d.Reasons[0], "an unknown source") {
		t.Errorf("decision = %+v", d)
	}
}
