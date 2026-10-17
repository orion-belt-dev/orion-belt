package common

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/crypto/ssh"
)

func TestServerConfig_EffectivePorts(t *testing.T) {
	var zero ServerConfig
	if zero.EffectiveAPIPort() != 8080 || zero.EffectiveSSHPort() != 2222 {
		t.Errorf("defaults = %d/%d, want 8080/2222", zero.EffectiveAPIPort(), zero.EffectiveSSHPort())
	}
	set := ServerConfig{APIPort: 9000, Port: 2022}
	if set.EffectiveAPIPort() != 9000 || set.EffectiveSSHPort() != 2022 {
		t.Errorf("configured = %d/%d, want 9000/2022", set.EffectiveAPIPort(), set.EffectiveSSHPort())
	}
	if got := (ServerConfig{Port: 2022}).AdvertisedSSHPort(); got != 2022 {
		t.Errorf("AdvertisedSSHPort without public port = %d, want listen port", got)
	}
	if got := (ServerConfig{Port: 2022, PublicSSHPort: 22}).AdvertisedSSHPort(); got != 22 {
		t.Errorf("AdvertisedSSHPort = %d, want public port 22", got)
	}
}

func TestServerConfig_AdvertisedURLFromAPIEndpoint(t *testing.T) {
	cases := map[string]string{
		"":                            "",
		"  ":                          "",
		"https://gw.example.com/api":  "https://gw.example.com",
		"https://gw.example.com/api/": "https://gw.example.com",
		"https://gw.example.com/":     "https://gw.example.com",
		"http://10.0.0.1:8080":        "http://10.0.0.1:8080",
	}
	for ep, want := range cases {
		if got := (ServerConfig{APIEndpoint: ep}).AdvertisedURL(); got != want {
			t.Errorf("AdvertisedURL(APIEndpoint=%q) = %q, want %q", ep, got, want)
		}
	}
}

func TestServerConfig_AdvertisedSSHHost(t *testing.T) {
	cases := []struct {
		name string
		cfg  ServerConfig
		want string
	}{
		{"explicit public ssh host", ServerConfig{PublicSSHHost: " ssh.example.com ", PublicURL: "https://ui.example.com"}, "ssh.example.com"},
		{"host from public url", ServerConfig{PublicURL: "https://ui.example.com:8443"}, "ui.example.com"},
		{"url without host falls through to bind host", ServerConfig{PublicURL: "relative/path", Host: "10.0.0.9"}, "10.0.0.9"},
		{"bind host", ServerConfig{Host: "gw.internal"}, "gw.internal"},
		{"wildcard v4 bind", ServerConfig{Host: "0.0.0.0"}, "localhost"},
		{"wildcard v6 bind", ServerConfig{Host: "::"}, "localhost"},
		{"bracketed wildcard v6 bind", ServerConfig{Host: "[::]"}, "localhost"},
		{"nothing set", ServerConfig{}, "localhost"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.cfg.AdvertisedSSHHost(); got != tc.want {
				t.Errorf("AdvertisedSSHHost() = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestServerConfig_UIURLUsesAdvertisedURL(t *testing.T) {
	if got := (ServerConfig{PublicURL: "https://gw.example.com/"}).UIURL(); got != "https://gw.example.com/ui" {
		t.Errorf("UIURL = %q", got)
	}
}

func TestWebAuthnDefaultsFromPublicURL_NoOpCases(t *testing.T) {
	ServerConfig{PublicURL: "https://x"}.WebAuthnDefaultsFromPublicURL(nil) // must not panic

	cases := map[string]ServerConfig{
		"no public url":    {},
		"url without host": {PublicURL: "relative"},
		"unparseable url":  {PublicURL: "http://[::1"},
	}
	for name, cfg := range cases {
		t.Run(name, func(t *testing.T) {
			wa := &WebAuthnConfig{}
			cfg.WebAuthnDefaultsFromPublicURL(wa)
			if wa.RPID != "" || len(wa.Origins) != 0 {
				t.Errorf("WebAuthn config = %+v, want untouched", wa)
			}
		})
	}
}

func TestWebAuthnDefaultsFromPublicURL_KeepsExplicitValues(t *testing.T) {
	wa := &WebAuthnConfig{RPID: "explicit.example.com", Origins: []string{"https://explicit.example.com"}}
	ServerConfig{PublicURL: "https://gw.example.com"}.WebAuthnDefaultsFromPublicURL(wa)
	if wa.RPID != "explicit.example.com" || len(wa.Origins) != 1 || wa.Origins[0] != "https://explicit.example.com" {
		t.Errorf("explicit values overwritten: %+v", wa)
	}
}

func TestSaveAndLoadConfig_RoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "server.yaml")
	in := &Config{
		Server:   ServerConfig{Host: "gw.internal", Port: 2022, APIPort: 9000},
		Database: DatabaseConfig{Driver: "postgres", ConnectionString: "postgres://x"},
		Plugins:  map[string]map[string]interface{}{"audit": {"enabled": true}},
	}
	if err := SaveConfig(path, in); err != nil {
		t.Fatalf("SaveConfig: %v", err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Errorf("config file mode = %o, want 600 (it can hold secrets)", info.Mode().Perm())
	}

	out, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if out.Server.Host != "gw.internal" || out.Server.Port != 2022 || out.Database.Driver != "postgres" || out.Plugins["audit"]["enabled"] != true {
		t.Errorf("round trip = %+v", out)
	}
}

func TestLoadConfig_Errors(t *testing.T) {
	dir := t.TempDir()
	if _, err := LoadConfig(filepath.Join(dir, "absent.yaml")); err == nil || !strings.Contains(err.Error(), "failed to read") {
		t.Errorf("LoadConfig(missing) error = %v, want read error", err)
	}
	bad := filepath.Join(dir, "bad.yaml")
	if err := os.WriteFile(bad, []byte("server: [unclosed"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadConfig(bad); err == nil || !strings.Contains(err.Error(), "failed to parse") {
		t.Errorf("LoadConfig(bad yaml) error = %v, want parse error", err)
	}
}

func TestSaveConfig_Errors(t *testing.T) {
	if err := SaveConfig(filepath.Join(t.TempDir(), "missing", "c.yaml"), &Config{}); err == nil || !strings.Contains(err.Error(), "failed to write") {
		t.Errorf("SaveConfig(missing dir) error = %v, want write error", err)
	}
	cfg := &Config{Plugins: map[string]map[string]interface{}{"p": {"v": failingYAML{}}}}
	if err := SaveConfig(filepath.Join(t.TempDir(), "c.yaml"), cfg); err == nil || !strings.Contains(err.Error(), "failed to marshal") {
		t.Errorf("SaveConfig(unencodable) error = %v, want marshal error", err)
	}
}

type failingYAML struct{}

func (failingYAML) MarshalYAML() (interface{}, error) { return nil, errors.New("cannot encode") }

func TestFormatTarget(t *testing.T) {
	if got := FormatTarget("", "web-01"); got != "root@web-01" {
		t.Errorf("FormatTarget default = %q", got)
	}
	if got := FormatTarget("deploy", "web-01"); got != "deploy@web-01" {
		t.Errorf("FormatTarget = %q", got)
	}
}

func TestParseGatewaySSHUser_EmptyTarget(t *testing.T) {
	auth, remote, machine := ParseGatewaySSHUser("alice+")
	if auth != "alice" || remote != "" || machine != "" {
		t.Errorf("ParseGatewaySSHUser(alice+) = %q %q %q", auth, remote, machine)
	}
}

func TestEffectiveAllowedChannels(t *testing.T) {
	var nilPolicy *NotificationPolicy
	if got := nilPolicy.EffectiveAllowedChannels(); len(got) != len(KnownNotificationChannels()) {
		t.Errorf("nil policy channels = %v, want all known", got)
	}
	p := &NotificationPolicy{AllowedChannels: []string{ChannelEmail, "carrier-pigeon"}}
	if got := p.EffectiveAllowedChannels(); len(got) != 1 || got[0] != ChannelEmail {
		t.Errorf("EffectiveAllowedChannels = %v, want [email]", got)
	}
}

func TestNotificationPolicyNilAndEmptyNormalize(t *testing.T) {
	var nilPolicy *NotificationPolicy
	nilPolicy.Normalize() // must not panic
	if nilPolicy.ClampPrefs(nil) {
		t.Error("ClampPrefs(nil) reported a change")
	}

	p := &NotificationPolicy{MandatoryEvents: map[string][]string{"  ": {ChannelEmail}, "x": {"unknown"}}}
	p.Normalize()
	if len(p.MandatoryEvents) != 0 {
		t.Errorf("MandatoryEvents = %v, want blank events and unknown-only channels dropped", p.MandatoryEvents)
	}
}

func TestNotificationPrefsChannelToggles(t *testing.T) {
	var nilPrefs *NotificationPrefs
	nilPrefs.setChannelEnabled(ChannelEmail, true) // must not panic
	if nilPrefs.channelEnabled(ChannelEmail) {
		t.Error("nil prefs report a channel enabled")
	}

	p := &NotificationPrefs{}
	p.setChannelEnabled("unknown", true)
	if p.channelEnabled("unknown") || p.InAppEnabled || p.EmailEnabled {
		t.Errorf("unknown channel toggled something: %+v", p)
	}
}

// --- host key callback -------------------------------------------------------

func TestNewHostKeyCallback_InvalidHostCAKey_ReturnsError(t *testing.T) {
	_, err := NewHostKeyCallback(HostKeyConfig{
		KnownHosts:      filepath.Join(t.TempDir(), "kh"),
		HostCAPublicKey: "not a key",
	}, nil)
	if err == nil || !strings.Contains(err.Error(), "host_ca_public_key") {
		t.Fatalf("error = %v, want a parse error naming the config key", err)
	}
}

func TestNewHostKeyCallback_InsecureModeAcceptsAnyKeyAndWarns(t *testing.T) {
	var logs bytes.Buffer
	cb, err := NewHostKeyCallback(HostKeyConfig{StrictHostKeyChecking: " NO "}, NewLoggerTo(DEBUG, &logs))
	if err != nil {
		t.Fatalf("NewHostKeyCallback: %v", err)
	}
	if err := cb("anything:22", &mockAddr{s: "1.2.3.4:22"}, mustTestPublicKey(t)); err != nil {
		t.Errorf("insecure callback rejected a key: %v", err)
	}
	if !strings.Contains(logs.String(), "verification disabled") {
		t.Errorf("log = %q, want a warning that verification is off", logs.String())
	}
}

func TestNewHostKeyCallback_KnownHostsPathResolution(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	t.Run("default path under ~/.ssh", func(t *testing.T) {
		if _, err := NewHostKeyCallback(HostKeyConfig{}, nil); err != nil {
			t.Fatalf("NewHostKeyCallback: %v", err)
		}
		if _, err := os.Stat(filepath.Join(home, ".ssh", "orion_known_hosts")); err != nil {
			t.Errorf("default known_hosts not created: %v", err)
		}
	})
	t.Run("tilde path expands to home", func(t *testing.T) {
		if _, err := NewHostKeyCallback(HostKeyConfig{KnownHosts: "~/custom/kh"}, nil); err != nil {
			t.Fatalf("NewHostKeyCallback: %v", err)
		}
		if _, err := os.Stat(filepath.Join(home, "custom", "kh")); err != nil {
			t.Errorf("~/custom/kh not created: %v", err)
		}
	})
	t.Run("missing HOME is an error", func(t *testing.T) {
		t.Setenv("HOME", "")
		if _, err := NewHostKeyCallback(HostKeyConfig{}, nil); err == nil {
			t.Error("default path without HOME succeeded")
		}
		if _, err := NewHostKeyCallback(HostKeyConfig{KnownHosts: "~/kh"}, nil); err == nil {
			t.Error("tilde path without HOME succeeded")
		}
	})
}

func TestNewHostKeyCallback_UnusableKnownHosts_ReturnsError(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "file")
	if err := os.WriteFile(file, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	corrupt := filepath.Join(dir, "corrupt")
	if err := os.WriteFile(corrupt, []byte("host.example.com ssh-rsa !!!notbase64\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	cases := map[string]struct{ path, want string }{
		"parent is a file":    {filepath.Join(file, "kh"), "create known_hosts directory"},
		"path is a directory": {dir, "create known_hosts file"},
		"corrupt contents":    {corrupt, "load known_hosts"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := NewHostKeyCallback(HostKeyConfig{KnownHosts: tc.path}, nil)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error = %v, want %q", err, tc.want)
			}
		})
	}
}

func TestHostKeyCallback_ChangedKeyIsAlwaysRejected(t *testing.T) {
	var logs bytes.Buffer
	kh := filepath.Join(t.TempDir(), "kh")
	cb, err := NewHostKeyCallback(HostKeyConfig{KnownHosts: kh}, NewLoggerTo(DEBUG, &logs))
	if err != nil {
		t.Fatal(err)
	}
	addr := &mockAddr{s: "127.0.0.1:2222"}
	if err := cb("gw.example.com:2222", addr, mustTestPublicKey(t)); err != nil {
		t.Fatalf("TOFU: %v", err)
	}
	if !strings.Contains(logs.String(), "TOFU: learned host key for gw.example.com:2222") {
		t.Errorf("log = %q, want the TOFU message", logs.String())
	}

	err = cb("gw.example.com:2222", addr, mustTestPublicKey(t))
	if err == nil || !strings.Contains(err.Error(), "REMOTE HOST IDENTIFICATION HAS CHANGED") {
		t.Fatalf("changed key error = %v, want the identification-changed warning", err)
	}
}

func TestHostKeyCallback_TOFUWriteFailure_RejectsConnection(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root ignores file permissions, so the write cannot be made to fail")
	}
	kh := filepath.Join(t.TempDir(), "kh")
	cb, err := NewHostKeyCallback(HostKeyConfig{KnownHosts: kh}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(kh, 0o400); err != nil {
		t.Fatal(err)
	}
	err = cb("gw.example.com:2222", &mockAddr{s: "127.0.0.1:2222"}, mustTestPublicKey(t))
	if err == nil || !strings.Contains(err.Error(), "failed to write known_hosts") {
		t.Fatalf("error = %v, want a TOFU write failure", err)
	}
}

func TestAppendKnownHost_SkipsDuplicateLines(t *testing.T) {
	kh := filepath.Join(t.TempDir(), "kh")
	signer := mustTestSigner(t)
	line := strings.TrimSpace("gw.example.com " + string(ssh.MarshalAuthorizedKey(signer.PublicKey())))

	for i := 0; i < 3; i++ {
		if err := appendKnownHost(kh, line); err != nil {
			t.Fatalf("appendKnownHost #%d: %v", i, err)
		}
	}
	data, _ := os.ReadFile(kh)
	if n := strings.Count(string(data), "\n"); n != 1 {
		t.Errorf("known_hosts has %d lines, want the entry exactly once", n)
	}
}
