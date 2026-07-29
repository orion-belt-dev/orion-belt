package common

import (
	"fmt"
	"os"

	"gopkg.in/yaml.v3"
)

// Config represents the base configuration structure
type Config struct {
	Server    ServerConfig                      `yaml:"server"`
	Database  DatabaseConfig                    `yaml:"database,omitempty"`
	Auth      AuthConfig                        `yaml:"auth,omitempty"`
	Agent     AgentConfig                       `yaml:"agent,omitempty"`
	Recording RecordingConfig                   `yaml:"recording,omitempty"`
	SSHCA     SSHCAConfig                       `yaml:"ssh_ca,omitempty"`
	Tracing   TracingConfig                     `yaml:"tracing,omitempty"`
	Plugins   map[string]map[string]interface{} `yaml:"plugins"`
}

// TracingConfig configures optional OpenTelemetry distributed tracing across
// the gateway -> agent -> target path, exported over OTLP. Shared by the
// gateway and the agent: both need to be enabled for a trace to span the whole
// path, and either can be enabled alone.
//
// Disabled unless Enabled is true, in which case no exporter is created and no
// tracer is installed. See pkg/tracing.
type TracingConfig struct {
	Enabled bool `yaml:"enabled"`
	// Endpoint is the OTLP collector address. Empty falls back to the
	// standard OTEL_EXPORTER_OTLP_* environment variables.
	Endpoint string `yaml:"endpoint,omitempty"`
	// Protocol is the OTLP transport: grpc (default) or http.
	Protocol string `yaml:"protocol,omitempty"`
	// Insecure disables TLS to the collector (localhost / trusted network).
	Insecure bool `yaml:"insecure,omitempty"`
	// ServiceName overrides the default per-binary service name.
	ServiceName string `yaml:"service_name,omitempty"`
	// SampleRatio is the head-sampling probability from 0.0 to 1.0. Unset
	// means 1.0. Sampling is parent-based, so the gateway's decision carries
	// to the agent and multi-hop traces stay whole.
	SampleRatio float64 `yaml:"sample_ratio,omitempty"`
	// Headers are extra OTLP headers, e.g. auth for a hosted collector.
	Headers map[string]string `yaml:"headers,omitempty"`
}

// ServerConfig contains server-specific configuration
type ServerConfig struct {
	Host           string `yaml:"host"`
	Port           int    `yaml:"port"`
	APIPort        int    `yaml:"api_port,omitempty"`
	SSHHostKey     string `yaml:"ssh_host_key,omitempty"`
	APIEndpoint    string `yaml:"api_endpoint,omitempty"`
	MetricsEnabled bool   `yaml:"metrics_enabled,omitempty"`
}

// DatabaseConfig contains database configuration
type DatabaseConfig struct {
	Driver           string `yaml:"driver"`
	ConnectionString string `yaml:"connection_string"`
}

// AuthConfig contains authentication configuration
type AuthConfig struct {
	KeyFile               string         `yaml:"key_file,omitempty"`
	User                  string         `yaml:"user,omitempty"`
	ReBACEnabled          bool           `yaml:"rebac_enabled,omitempty"`
	AllowTempAccess       bool           `yaml:"allow_temp_access,omitempty"`
	KnownHosts            string         `yaml:"known_hosts,omitempty"`
	StrictHostKeyChecking string         `yaml:"strict_host_key_checking,omitempty"` // yes | ask | no
	JWTSecret             string         `yaml:"jwt_secret,omitempty"`
	JWTExpiryHours        int            `yaml:"jwt_expiry_hours,omitempty"`
	MFARequired           bool           `yaml:"mfa_required,omitempty"`
	RateLimitPerMinute    int            `yaml:"rate_limit_per_minute,omitempty"` // API requests per user/IP; default 600
	OpenFGA               OpenFGAConfig  `yaml:"openfga,omitempty"`
	WebAuthn              WebAuthnConfig `yaml:"webauthn,omitempty"`
	HostCAPublicKey       string         `yaml:"host_ca_public_key,omitempty"` // trusted Host CA pubkey (authorized_keys line); empty = pure TOFU
}

// WebAuthnConfig configures FIDO2/WebAuthn (YubiKey, etc.).
type WebAuthnConfig struct {
	Enabled   bool     `yaml:"enabled"`
	RPDisplay string   `yaml:"rp_display_name"` // e.g. Orion Belt
	RPID      string   `yaml:"rp_id"`           // e.g. orion.example.com
	Origins   []string `yaml:"origins"`         // e.g. https://orion.example.com
}

// OpenFGAConfig configures optional OpenFGA authorization.
type OpenFGAConfig struct {
	Enabled  bool   `yaml:"enabled"`
	APIURL   string `yaml:"api_url"`
	StoreID  string `yaml:"store_id"`
	ModelID  string `yaml:"model_id,omitempty"`
	APIToken string `yaml:"api_token,omitempty"`
	Relation string `yaml:"relation,omitempty"` // default: can_access
}

// AgentConfig contains agent-specific configuration
type AgentConfig struct {
	Name string            `yaml:"name"`
	Tags map[string]string `yaml:"tags,omitempty"`
}

// RecordingConfig contains session recording configuration
type RecordingConfig struct {
	Enabled       bool   `yaml:"enabled"`
	StoragePath   string `yaml:"storage_path"`
	RetentionDays int    `yaml:"retention_days,omitempty"`
	EncryptionKey string `yaml:"encryption_key,omitempty"` // 32-byte key, base64 or raw 32 chars
	// Compression: gzip | none (default gzip for new recordings)
	Compression string `yaml:"compression,omitempty"`
}

// SSHCAConfig configures the server-side SSH Certificate Authority: short-
// lived signed user certs replacing static pubkey login, and a Host CA
// signing the gateway's own host key plus agent identity certs.
type SSHCAConfig struct {
	Enabled bool `yaml:"enabled"`
	// MasterKey encrypts the CA private key material at rest (32-byte,
	// base64 or raw). Required whenever Enabled is true: unlike
	// Recording.EncryptionKey, there is no plaintext fallback here — the
	// blast radius of a leaked CA private key (mint certs for any
	// principal, indefinitely) is categorically worse than a leaked
	// recording, so this is deliberately not optional.
	MasterKey string `yaml:"master_key,omitempty"`
	// UserCertTTLHours is the default lifetime of an issued user cert
	// (12h when unset).
	UserCertTTLHours int `yaml:"user_cert_ttl_hours,omitempty"`
	// MaxUserCertTTLHours caps a caller-requested TTL.
	MaxUserCertTTLHours int `yaml:"max_user_cert_ttl_hours,omitempty"`
	// HostCertTTLHours is the lifetime of the gateway's own host cert and
	// of agent identity certs. Long-lived by default; renewed
	// automatically well before expiry (see pkg/ca RenewalMargin).
	HostCertTTLHours int `yaml:"host_cert_ttl_hours,omitempty"`
	// HostPrincipals lists the hostnames/IPs clients actually use to reach
	// this gateway, embedded in its own host cert. server.host is often a
	// bind-all address (0.0.0.0) and not meaningful here, so this is
	// separate; defaults to [server.host] if left empty (fine for
	// single-hostname deployments where server.host is a real address).
	HostPrincipals []string `yaml:"host_principals,omitempty"`
}

// LoadConfig loads configuration from a YAML file
func LoadConfig(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read config file: %w", err)
	}

	var config Config
	if err := yaml.Unmarshal(data, &config); err != nil {
		return nil, fmt.Errorf("failed to parse config file: %w", err)
	}

	return &config, nil
}

// SaveConfig saves configuration to a YAML file
func SaveConfig(path string, config *Config) error {
	data, err := yaml.Marshal(config)
	if err != nil {
		return fmt.Errorf("failed to marshal config: %w", err)
	}

	if err := os.WriteFile(path, data, 0600); err != nil {
		return fmt.Errorf("failed to write config file: %w", err)
	}

	return nil
}
