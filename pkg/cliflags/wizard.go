package cliflags

import (
	"bufio"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"golang.org/x/crypto/ssh"
	"golang.org/x/term"
	"gopkg.in/yaml.v3"

	"github.com/orion-belt-dev/orion-belt/pkg/common"
)

// LoadConfig reads YAML or runs an interactive setup wizard when the file
// is missing, then applies flag/env overrides.
func (c *Common) LoadConfig() (*common.Config, error) {
	path := expandPath(c.ConfigFile)

	var cfg *common.Config
	if _, err := os.Stat(path); os.IsNotExist(err) {
		wcfg, werr := runClientWizard(path, c, os.Stdin, os.Stdout)
		if werr != nil {
			return nil, werr
		}
		cfg = wcfg
	} else {
		loaded, lerr := common.LoadConfig(path)
		if lerr != nil {
			return nil, lerr
		}
		cfg = loaded
	}

	c.applyOverrides(cfg)
	return cfg, nil
}

func (c *Common) applyOverrides(cfg *common.Config) {
	if v := os.Getenv("ORION_API_ENDPOINT"); v != "" && c.APIEndpoint == "" {
		c.APIEndpoint = v
	}
	if v := os.Getenv("ORION_USER"); v != "" && c.User == "" {
		c.User = v
	}

	if c.APIEndpoint != "" {
		cfg.Server.APIEndpoint = c.APIEndpoint
	}
	if c.ProxyHost != "" {
		cfg.Server.Host = c.ProxyHost
	}
	if c.ProxyPort > 0 {
		cfg.Server.Port = c.ProxyPort
	}
	if c.Identity != "" {
		cfg.Auth.KeyFile = c.Identity
	}
	if c.User != "" {
		cfg.Auth.User = c.User
	}
	if c.Insecure {
		cfg.Auth.StrictHostKeyChecking = "no"
	}
	cfg.Auth.KeyFile = expandPath(cfg.Auth.KeyFile)
	cfg.Auth.KnownHosts = expandPath(cfg.Auth.KnownHosts)
}

func expandPath(p string) string {
	if p == "" {
		return p
	}
	if strings.HasPrefix(p, "~/") {
		home, err := os.UserHomeDir()
		if err == nil {
			return filepath.Join(home, p[2:])
		}
	}
	return os.ExpandEnv(p)
}

func runClientWizard(path string, flags *Common, in io.Reader, out io.Writer) (*common.Config, error) {
	if !isInteractive() {
		return nil, fmt.Errorf("no client config at %s — create one or run this command in a terminal to use the setup wizard", path)
	}

	fmt.Fprintln(out, "No client config found. Let's set one up.")
	fmt.Fprintf(out, "  (will write %s)\n\n", path)

	reader := bufio.NewReader(in)

	hostDefault := "localhost"
	if flags.ProxyHost != "" {
		hostDefault = flags.ProxyHost
	}
	portDefault := 2222
	if flags.ProxyPort > 0 {
		portDefault = flags.ProxyPort
	}
	apiDefault := flags.APIEndpoint
	if apiDefault == "" {
		apiDefault = fmt.Sprintf("http://%s:8080", hostDefault)
	}
	userDefault := flags.User
	if userDefault == "" {
		userDefault = os.Getenv("ORION_USER")
	}
	if userDefault == "" {
		userDefault = os.Getenv("USER")
	}
	keyDefault := flags.Identity
	if keyDefault == "" {
		keyDefault = guessSSHKey()
	}

	host := prompt(reader, out, "Gateway SSH host", hostDefault)
	portStr := prompt(reader, out, "Gateway SSH port", strconv.Itoa(portDefault))
	port, err := strconv.Atoi(portStr)
	if err != nil || port <= 0 || port > 65535 {
		return nil, fmt.Errorf("invalid SSH port %q", portStr)
	}

	api := strings.TrimRight(prompt(reader, out, "API base URL (no trailing /api)", apiDefault), "/")
	user := prompt(reader, out, "Your gateway username", userDefault)
	if user == "" {
		return nil, fmt.Errorf("username is required")
	}
	keyPath := expandPath(prompt(reader, out, "SSH private key path", keyDefault))

	fmt.Fprintln(out)
	fmt.Fprintln(out, "Checking…")

	if err := checkPrivateKey(keyPath); err != nil {
		fmt.Fprintf(out, "  ✗ private key: %v\n", err)
		fmt.Fprintln(out, "    Fix the path (or generate a key) and re-run.")
		return nil, fmt.Errorf("private key check failed: %w", err)
	}
	fmt.Fprintf(out, "  ✓ private key readable (%s)\n", keyPath)

	if err := checkAPI(api, flags.Timeout); err != nil {
		fmt.Fprintf(out, "  ✗ API at %s: %v\n", api, err)
		fmt.Fprintln(out, "    Start the gateway (or fix the URL) and run again.")
		retry := prompt(reader, out, "Save config anyway? [y/N]", "N")
		if !isYes(retry) {
			return nil, fmt.Errorf("API check failed: %w", err)
		}
		fmt.Fprintln(out, "  ! saving without a working API — login will fail until the gateway is up")
	} else {
		fmt.Fprintf(out, "  ✓ API reachable (%s)\n", api)
	}

	if err := checkTCP(host, port, 3*time.Second); err != nil {
		fmt.Fprintf(out, "  ! SSH %s:%d not answering yet (%v) — OK if the gateway isn't listening on SSH from here\n", host, port, err)
	} else {
		fmt.Fprintf(out, "  ✓ SSH port open (%s:%d)\n", host, port)
	}

	strict := "ask"
	if flags.Insecure {
		strict = "no"
	}

	cfg := &common.Config{
		Server: common.ServerConfig{
			Host:        host,
			Port:        port,
			APIEndpoint: api,
		},
		Auth: common.AuthConfig{
			User:                  user,
			KeyFile:               keyPath,
			KnownHosts:            expandPath("~/.ssh/orion_known_hosts"),
			StrictHostKeyChecking: strict,
		},
	}

	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return nil, fmt.Errorf("create config dir: %w", err)
	}
	// Prefer a small commented template over raw yaml.Marshal for readability.
	if err := writeClientConfig(path, cfg); err != nil {
		return nil, err
	}
	fmt.Fprintf(out, "\nSaved %s\n\n", path)
	return cfg, nil
}

func writeClientConfig(path string, cfg *common.Config) error {
	type wire struct {
		Server common.ServerConfig `yaml:"server"`
		Auth   common.AuthConfig   `yaml:"auth"`
	}
	data, err := yaml.Marshal(&wire{Server: cfg.Server, Auth: cfg.Auth})
	if err != nil {
		return err
	}
	header := "# Orion Belt client config (generated by setup wizard)\n"
	return os.WriteFile(path, append([]byte(header), data...), 0600)
}

func prompt(r *bufio.Reader, w io.Writer, label, def string) string {
	if def != "" {
		fmt.Fprintf(w, "%s [%s]: ", label, def)
	} else {
		fmt.Fprintf(w, "%s: ", label)
	}
	line, err := r.ReadString('\n')
	if err != nil && len(strings.TrimSpace(line)) == 0 {
		return def
	}
	line = strings.TrimSpace(line)
	if line == "" {
		return def
	}
	return line
}

func isYes(s string) bool {
	s = strings.ToLower(strings.TrimSpace(s))
	return s == "y" || s == "yes"
}

func isInteractive() bool {
	return term.IsTerminal(int(os.Stdin.Fd())) && term.IsTerminal(int(os.Stdout.Fd()))
}

func guessSSHKey() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return "~/.ssh/id_ed25519"
	}
	for _, name := range []string{"id_ed25519", "id_ecdsa", "id_rsa"} {
		p := filepath.Join(home, ".ssh", name)
		if st, err := os.Stat(p); err == nil && !st.IsDir() {
			return p
		}
	}
	return filepath.Join(home, ".ssh", "id_ed25519")
}

func checkPrivateKey(path string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	_, err = ssh.ParsePrivateKey(data)
	if err != nil {
		// passphrase-protected keys still count as valid for the wizard
		if _, ok := err.(*ssh.PassphraseMissingError); ok {
			return nil
		}
		return fmt.Errorf("parse key: %w", err)
	}
	return nil
}

func checkAPI(base string, timeout time.Duration) error {
	if timeout <= 0 {
		timeout = 8 * time.Second
	}
	client := &http.Client{Timeout: timeout}
	base = strings.TrimRight(base, "/")
	urls := []string{
		base + "/api/v1/version",
		base + "/health",
	}
	var last error
	for _, u := range urls {
		resp, err := client.Get(u)
		if err != nil {
			last = err
			continue
		}
		resp.Body.Close()
		if resp.StatusCode >= 200 && resp.StatusCode < 500 {
			return nil
		}
		last = fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	if last == nil {
		last = fmt.Errorf("no response")
	}
	return last
}

func checkTCP(host string, port int, timeout time.Duration) error {
	conn, err := net.DialTimeout("tcp", net.JoinHostPort(host, strconv.Itoa(port)), timeout)
	if err != nil {
		return err
	}
	_ = conn.Close()
	return nil
}
