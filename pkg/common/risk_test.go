package common

import (
	"strings"
	"testing"
	"time"
)

func TestIsPCIInScope(t *testing.T) {
	for tag, want := range map[string]bool{"in-scope": true, " In-Scope ": true, "no": false, "": false} {
		if got := IsPCIInScope(&Machine{Tags: map[string]string{PCITagKey: tag}}); got != want {
			t.Errorf("pci=%q: got %v want %v", tag, got, want)
		}
	}
	if IsPCIInScope(&Machine{}) || IsPCIInScope(nil) {
		t.Error("untagged machines are out of scope")
	}
}

func TestMachineSensitivity(t *testing.T) {
	cases := []struct {
		tags map[string]string
		want string
	}{
		{map[string]string{SensitivityTagKey: "High"}, SensitivityHigh},
		{map[string]string{SensitivityTagKey: "low", PCITagKey: PCITagValue}, SensitivityLow},
		{map[string]string{PCITagKey: PCITagValue}, SensitivityHigh},
		{map[string]string{SensitivityTagKey: "critical"}, SensitivityUnset},
		{nil, SensitivityUnset},
	}
	for _, c := range cases {
		if got := MachineSensitivity(&Machine{Tags: c.tags}); got != c.want {
			t.Errorf("%v: got %s want %s", c.tags, got, c.want)
		}
	}
}

func TestNewRiskSignals(t *testing.T) {
	at := time.Date(2026, 10, 24, 3, 15, 0, 0, time.FixedZone("CEST", 2*3600))
	s := NewRiskSignals("192.0.2.4", strings.Repeat("x", 500), "api_key",
		&User{WebAuthnEnabled: true}, &Machine{Tags: map[string]string{SensitivityTagKey: "medium"}}, at)
	if s.HourUTC != 1 || s.Weekday != "Saturday" || !s.CollectedAt.Equal(at) {
		t.Errorf("time signals = %d %s %v", s.HourUTC, s.Weekday, s.CollectedAt)
	}
	if !s.MFAEnrolled || s.TargetSensitivity != SensitivityMedium || len(s.Device) != maxDeviceLength {
		t.Errorf("signals = %+v", s)
	}
	if NewRiskSignals("", "", "", nil, nil, at).MFAEnrolled {
		t.Error("an unknown user is not MFA-enrolled")
	}
}
