package common

import (
	"strings"
	"time"
)

// Machine tags that describe how sensitive a target is.
const (
	// SensitivityTagKey holds low, medium or high: tags: {sensitivity: high}.
	SensitivityTagKey = "sensitivity"
	// PCITagKey and PCITagValue mark a machine as PCI-in-scope:
	// tags: {pci: in-scope}. In-scope machines count as high sensitivity
	// unless tagged otherwise.
	PCITagKey   = "pci"
	PCITagValue = "in-scope"
)

// Target sensitivity levels.
const (
	SensitivityLow    = "low"
	SensitivityMedium = "medium"
	SensitivityHigh   = "high"
	SensitivityUnset  = "unset"
)

// IsPCIInScope reports whether a machine carries the pci: in-scope tag.
func IsPCIInScope(m *Machine) bool {
	if m == nil {
		return false
	}
	v, ok := m.Tags[PCITagKey]
	return ok && strings.EqualFold(strings.TrimSpace(v), PCITagValue)
}

// MachineSensitivity returns the machine's sensitivity tag, high for an
// untagged PCI-in-scope machine, and unset otherwise.
func MachineSensitivity(m *Machine) string {
	if m == nil {
		return SensitivityUnset
	}
	switch v := strings.ToLower(strings.TrimSpace(m.Tags[SensitivityTagKey])); v {
	case SensitivityLow, SensitivityMedium, SensitivityHigh:
		return v
	}
	if IsPCIInScope(m) {
		return SensitivityHigh
	}
	return SensitivityUnset
}

// RiskSignals are captured when an access request is made, as input for
// risk-based approval. Device and AuthMethod are best effort and empty when
// the client did not say.
type RiskSignals struct {
	SourceIP          string    `json:"source_ip,omitempty"`
	Device            string    `json:"device,omitempty"`      // the client's User-Agent
	AuthMethod        string    `json:"auth_method,omitempty"` // how the request was authenticated
	MFAEnrolled       bool      `json:"mfa_enrolled"`          // TOTP or WebAuthn on the account
	HourUTC           int       `json:"hour_utc"`
	Weekday           string    `json:"weekday"`
	TargetSensitivity string    `json:"target_sensitivity"`
	CollectedAt       time.Time `json:"collected_at"`
}

// maxDeviceLength bounds the stored User-Agent; the header is client-chosen.
const maxDeviceLength = 200

// NewRiskSignals builds the signals for a request made at now.
func NewRiskSignals(sourceIP, userAgent, authMethod string, user *User, target *Machine, now time.Time) *RiskSignals {
	now = now.UTC()
	device := strings.TrimSpace(userAgent)
	if len(device) > maxDeviceLength {
		device = device[:maxDeviceLength]
	}
	return &RiskSignals{
		SourceIP:          sourceIP,
		Device:            device,
		AuthMethod:        authMethod,
		MFAEnrolled:       user != nil && (user.MFAEnabled || user.WebAuthnEnabled),
		HourUTC:           now.Hour(),
		Weekday:           now.Weekday().String(),
		TargetSensitivity: MachineSensitivity(target),
		CollectedAt:       now,
	}
}
