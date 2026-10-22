package cliflags

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/spf13/cobra"

	"github.com/orion-belt-dev/orion-belt/pkg/common"
)

// --- seams ---------------------------------------------------------------------

func setInteractive(t *testing.T, v bool) {
	t.Helper()
	prev := isInteractive
	isInteractive = func() bool { return v }
	t.Cleanup(func() { isInteractive = prev })
}

func setPassword(t *testing.T, answer string, err error) {
	t.Helper()
	prev := readPassword
	readPassword = func() ([]byte, error) { return []byte(answer), err }
	t.Cleanup(func() { readPassword = prev })
}

// withStdin replaces os.Stdin with a pipe pre-filled with input.
func withStdin(t *testing.T, input string) {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.WriteString(input); err != nil {
		t.Fatal(err)
	}
	w.Close()
	prev := os.Stdin
	os.Stdin = r
	t.Cleanup(func() { os.Stdin = prev; r.Close() })
}

// withStderr captures os.Stderr for the duration of the test.
func withStderr(t *testing.T) func() string {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	prev := os.Stderr
	os.Stderr = w
	t.Cleanup(func() { os.Stderr = prev })
	return func() string {
		os.Stderr = prev
		w.Close()
		var b bytes.Buffer
		_, _ = b.ReadFrom(r)
		return b.String()
	}
}

func apiServer(t *testing.T, status int) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(status)
	}))
	t.Cleanup(srv.Close)
	return srv
}

func listenTCP(t *testing.T) (string, int) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	addr := ln.Addr().(*net.TCPAddr)
	return "127.0.0.1", addr.Port
}

// --- flags ---------------------------------------------------------------------

func TestBindPersistentAndSSHTrust(t *testing.T) {
	t.Setenv("ORION_CONFIG", "/etc/orion/client.yaml")
	var c Common
	var ranExisting bool
	cmd := &cobra.Command{Use: "osh", RunE: func(*cobra.Command, []string) error { return nil }}
	cmd.PersistentPreRunE = func(*cobra.Command, []string) error { ranExisting = true; return nil }
	c.BindPersistent(cmd)
	c.BindSSHTrust(cmd)

	cmd.SetArgs([]string{"--no-host-key-check", "--proxy", "gw", "--proxy-port", "2022", "-u", "alice", "--timeout", "5s"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !ranExisting {
		t.Error("BindSSHTrust replaced the existing PersistentPreRunE instead of chaining it")
	}
	if !c.Insecure || c.ProxyHost != "gw" || c.ProxyPort != 2022 || c.User != "alice" || c.Timeout != 5*time.Second {
		t.Errorf("parsed flags = %+v", c)
	}
	if c.ConfigFile != "/etc/orion/client.yaml" {
		t.Errorf("config default = %q, want $ORION_CONFIG", c.ConfigFile)
	}
}

func TestChainPreRun_StopsOnFirstError(t *testing.T) {
	boom := errors.New("boom")
	ranNext := false
	f := chainPreRun(func(*cobra.Command, []string) error { return boom }, func(*cobra.Command, []string) error { ranNext = true; return nil })
	if err := f(nil, nil); !errors.Is(err, boom) || ranNext {
		t.Errorf("chainPreRun = %v, next ran=%v; want the first error and no second call", err, ranNext)
	}
}

func TestDefaultConfigPath_UsesHome(t *testing.T) {
	t.Setenv("ORION_CONFIG", "")
	t.Setenv("HOME", "/home/test")
	if got := defaultConfigPath(); got != "/home/test/.orion-belt/client.yaml" {
		t.Errorf("defaultConfigPath = %q", got)
	}
}

func TestUsername_Precedence(t *testing.T) {
	cfg := &common.Config{}
	t.Setenv("ORION_USER", "env-orion")
	t.Setenv("USER", "env-user")

	if u, _ := (&Common{User: "flag"}).Username(cfg); u != "flag" {
		t.Errorf("flag user = %q", u)
	}
	cfg.Auth.User = "config"
	if u, _ := (&Common{}).Username(cfg); u != "config" {
		t.Errorf("config user = %q", u)
	}
	cfg.Auth.User = ""
	if u, _ := (&Common{}).Username(cfg); u != "env-orion" {
		t.Errorf("$ORION_USER = %q", u)
	}
	t.Setenv("ORION_USER", "")
	if u, _ := (&Common{}).Username(cfg); u != "env-user" {
		t.Errorf("$USER = %q", u)
	}
}

// --- config loading ------------------------------------------------------------

func TestLoadConfig_ExistingFileWithOverrides(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("ORION_API_ENDPOINT", "https://env.example.com")
	t.Setenv("ORION_USER", "env-user")

	path := filepath.Join(home, "client.yaml")
	if err := common.SaveConfig(path, &common.Config{
		Server: common.ServerConfig{Host: "file-host", Port: 2222},
		Auth:   common.AuthConfig{KeyFile: "~/.ssh/id_ed25519", KnownHosts: "$HOME/kh"},
	}); err != nil {
		t.Fatal(err)
	}

	c := &Common{ConfigFile: path, ProxyHost: "flag-host", ProxyPort: 2022, Identity: "~/other_key", Insecure: true}
	cfg, err := c.LoadConfig()
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	want := common.AuthConfig{
		KeyFile:               filepath.Join(home, "other_key"),
		KnownHosts:            filepath.Join(home, "kh"),
		User:                  "env-user",
		StrictHostKeyChecking: "no",
	}
	if !reflect.DeepEqual(cfg.Auth, want) {
		t.Errorf("Auth = %+v\nwant %+v", cfg.Auth, want)
	}
	if cfg.Server.Host != "flag-host" || cfg.Server.Port != 2022 || cfg.Server.APIEndpoint != "https://env.example.com" {
		t.Errorf("Server = %+v", cfg.Server)
	}
}

func TestLoadConfig_ExplicitFlagsBeatEnvironment(t *testing.T) {
	t.Setenv("ORION_API_ENDPOINT", "https://env.example.com")
	t.Setenv("ORION_USER", "env-user")
	c := &Common{APIEndpoint: "https://flag.example.com", User: "flag-user"}
	cfg := &common.Config{}
	c.applyOverrides(cfg)
	if cfg.Server.APIEndpoint != "https://flag.example.com" || cfg.Auth.User != "flag-user" {
		t.Errorf("overrides = %+v / %+v", cfg.Server, cfg.Auth)
	}
}

func TestLoadConfig_Errors(t *testing.T) {
	dir := t.TempDir()
	bad := filepath.Join(dir, "bad.yaml")
	if err := os.WriteFile(bad, []byte("server: ["), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := (&Common{ConfigFile: bad}).LoadConfig(); err == nil {
		t.Error("LoadConfig of malformed YAML succeeded")
	}

	setInteractive(t, false)
	_, err := (&Common{ConfigFile: filepath.Join(dir, "absent.yaml")}).LoadConfig()
	if err == nil || !strings.Contains(err.Error(), "no client config at") {
		t.Errorf("LoadConfig without a file or terminal = %v, want the no-config error", err)
	}
}

func TestLoadConfig_MissingFileRunsWizardOnStdin(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	setInteractive(t, true)
	key := writeTestKey(t, home)
	host, port := listenTCP(t)
	api := apiServer(t, http.StatusOK)

	withStdin(t, strings.Join([]string{host, strconv.Itoa(port), api.URL, "alice", key}, "\n")+"\n")
	path := filepath.Join(home, "new", "client.yaml")

	stdout := os.Stdout
	devnull, _ := os.Open(os.DevNull)
	os.Stdout = devnull
	cfg, err := (&Common{ConfigFile: path, Timeout: time.Second}).LoadConfig()
	os.Stdout = stdout
	devnull.Close()

	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if cfg.Auth.User != "alice" || cfg.Server.Port != port {
		t.Errorf("wizard config = %+v / %+v", cfg.Server, cfg.Auth)
	}
	if _, err := os.Stat(path); err != nil {
		t.Errorf("wizard did not write %s: %v", path, err)
	}
}

// --- wizard --------------------------------------------------------------------

func runWizard(t *testing.T, flags *Common, answers ...string) (*common.Config, string, error) {
	t.Helper()
	setInteractive(t, true)
	var out bytes.Buffer
	path := filepath.Join(t.TempDir(), "conf", "client.yaml")
	cfg, err := runClientWizard(path, flags, strings.NewReader(strings.Join(answers, "\n")+"\n"), &out)
	return cfg, out.String(), err
}

func TestRunClientWizard_HappyPathWritesConfig(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	key := writeTestKey(t, home)
	host, port := listenTCP(t)
	api := apiServer(t, http.StatusOK)

	setInteractive(t, true)
	var out bytes.Buffer
	path := filepath.Join(home, "conf", "client.yaml")
	input := strings.Join([]string{host, strconv.Itoa(port), api.URL + "/", "alice", key}, "\n") + "\n"
	cfg, err := runClientWizard(path, &Common{Timeout: time.Second}, strings.NewReader(input), &out)
	if err != nil {
		t.Fatalf("runClientWizard: %v\n%s", err, out.String())
	}

	if cfg.Server.APIEndpoint != api.URL || cfg.Auth.StrictHostKeyChecking != "ask" || cfg.Auth.KeyFile != key {
		t.Errorf("config = %+v / %+v", cfg.Server, cfg.Auth)
	}
	for _, want := range []string{"✓ private key readable", "✓ API reachable", "✓ SSH port open", "Saved " + path} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("wizard output lacks %q:\n%s", want, out.String())
		}
	}
	loaded, err := common.LoadConfig(path)
	if err != nil || loaded.Auth.User != "alice" {
		t.Errorf("saved config = %+v, %v", loaded, err)
	}
	if info, _ := os.Stat(path); info.Mode().Perm() != 0o600 {
		t.Errorf("config mode = %o, want 600", info.Mode().Perm())
	}
}

func TestRunClientWizard_UsesFlagDefaultsOnEmptyAnswers(t *testing.T) {
	home := t.TempDir()
	key := writeTestKey(t, home)
	host, port := listenTCP(t)
	api := apiServer(t, http.StatusOK)

	flags := &Common{ProxyHost: host, ProxyPort: port, APIEndpoint: api.URL, User: "bob", Identity: key, Insecure: true, Timeout: time.Second}
	cfg, out, err := runWizard(t, flags, "", "", "", "", "")
	if err != nil {
		t.Fatalf("runClientWizard: %v\n%s", err, out)
	}
	if cfg.Server.Host != host || cfg.Auth.User != "bob" || cfg.Auth.StrictHostKeyChecking != "no" {
		t.Errorf("config = %+v / %+v, want flag defaults and insecure", cfg.Server, cfg.Auth)
	}
	if !strings.Contains(out, "Gateway SSH host ["+host+"]") {
		t.Errorf("prompt did not show the flag default:\n%s", out)
	}
}

func TestRunClientWizard_Failures(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	key := writeTestKey(t, home)
	garbage := filepath.Join(home, "garbage")
	if err := os.WriteFile(garbage, []byte("not a key"), 0o600); err != nil {
		t.Fatal(err)
	}
	down := "http://127.0.0.1:1"

	cases := []struct {
		name    string
		answers []string
		want    string
	}{
		{"invalid port", []string{"h", "99999"}, "invalid SSH port"},
		{"non-numeric port", []string{"h", "ssh"}, "invalid SSH port"},
		{"missing key", []string{"h", "22", down, "alice", filepath.Join(home, "absent")}, "private key check failed"},
		{"unparseable key", []string{"h", "22", down, "alice", garbage}, "parse key"},
		{"api down and not saved", []string{"h", "22", down, "alice", key, "n"}, "API check failed"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, out, err := runWizard(t, &Common{Timeout: 200 * time.Millisecond}, tc.answers...)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error = %v, want %q\n%s", err, tc.want, out)
			}
		})
	}
}

func TestRunClientWizard_EmptyUsernameIsRejected(t *testing.T) {
	t.Setenv("ORION_USER", "")
	t.Setenv("USER", "")
	_, _, err := runWizard(t, &Common{}, "h", "22", "http://x", "")
	if err == nil || !strings.Contains(err.Error(), "username is required") {
		t.Errorf("error = %v, want username required", err)
	}
}

func TestRunClientWizard_ApiDownButSavedAnyway(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	key := writeTestKey(t, home)

	// Port 1 on loopback refuses both the API check and the SSH probe.
	cfg, out, err := runWizard(t, &Common{Timeout: 200 * time.Millisecond}, "127.0.0.1", "1", "http://127.0.0.1:1", "alice", key, "yes")
	if err != nil {
		t.Fatalf("runClientWizard: %v\n%s", err, out)
	}
	if cfg == nil || !strings.Contains(out, "saving without a working API") || !strings.Contains(out, "not answering yet") {
		t.Errorf("output lacks the degraded-save warnings:\n%s", out)
	}
}

func TestRunClientWizard_UnwritableConfigDir(t *testing.T) {
	home := t.TempDir()
	key := writeTestKey(t, home)
	api := apiServer(t, http.StatusOK)
	file := filepath.Join(home, "file")
	if err := os.WriteFile(file, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	setInteractive(t, true)
	in := strings.NewReader(strings.Join([]string{"127.0.0.1", "1", api.URL, "alice", key}, "\n") + "\n")
	_, err := runClientWizard(filepath.Join(file, "sub", "c.yaml"), &Common{Timeout: time.Second}, in, &bytes.Buffer{})
	if err == nil || !strings.Contains(err.Error(), "create config dir") {
		t.Errorf("error = %v, want create config dir failure", err)
	}

	setInteractive(t, true)
	in = strings.NewReader(strings.Join([]string{"127.0.0.1", "1", api.URL, "alice", key}, "\n") + "\n")
	dir := filepath.Join(home, "isdir")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := runClientWizard(dir, &Common{Timeout: time.Second}, in, &bytes.Buffer{}); err == nil {
		t.Error("writing the config over a directory succeeded")
	}
}

func TestPrompt_EOFFallsBackToDefault(t *testing.T) {
	var out bytes.Buffer
	if got := prompt(bufioReader(""), &out, "Name", "def"); got != "def" {
		t.Errorf("prompt at EOF = %q, want default", got)
	}
	if got := prompt(bufioReader("partial"), &out, "No default", ""); got != "partial" {
		t.Errorf("prompt with unterminated line = %q, want the line", got)
	}
	if !strings.Contains(out.String(), "No default: ") {
		t.Errorf("prompt without default rendered %q", out.String())
	}
}

func TestIsYes(t *testing.T) {
	for in, want := range map[string]bool{"y": true, " YES ": true, "n": false, "": false, "yep": false} {
		if got := isYes(in); got != want {
			t.Errorf("isYes(%q) = %v, want %v", in, got, want)
		}
	}
}

func TestGuessSSHKey(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	if got := guessSSHKey(); got != filepath.Join(home, ".ssh", "id_ed25519") {
		t.Errorf("guessSSHKey with no keys = %q, want the ed25519 default", got)
	}
	if err := os.MkdirAll(filepath.Join(home, ".ssh"), 0o700); err != nil {
		t.Fatal(err)
	}
	rsa := filepath.Join(home, ".ssh", "id_rsa")
	if err := os.WriteFile(rsa, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if got := guessSSHKey(); got != rsa {
		t.Errorf("guessSSHKey = %q, want the existing id_rsa", got)
	}
	t.Setenv("HOME", "")
	if got := guessSSHKey(); got != "~/.ssh/id_ed25519" {
		t.Errorf("guessSSHKey without HOME = %q", got)
	}
}

func TestCheckAPI_StatusHandling(t *testing.T) {
	if err := checkAPI(apiServer(t, http.StatusUnauthorized).URL, 0); err != nil {
		t.Errorf("checkAPI with 401 = %v, want reachable", err)
	}
	err := checkAPI(apiServer(t, http.StatusBadGateway).URL, time.Second)
	if err == nil || err.Error() != "HTTP 502" {
		t.Errorf("checkAPI with 502 = %v, want HTTP 502", err)
	}
}

// --- prompts, browser, output ----------------------------------------------------

func TestPromptLine(t *testing.T) {
	stderr := withStderr(t)
	withStdin(t, "  answer  \n")
	got, err := PromptLine("Question: ")
	if err != nil || got != "answer" {
		t.Errorf("PromptLine = %q, %v", got, err)
	}
	if !strings.Contains(stderr(), "Question: ") {
		t.Error("PromptLine did not print its label to stderr")
	}

	withStdin(t, "")
	if _, err := PromptLine("Q: "); err == nil {
		t.Error("PromptLine at EOF succeeded")
	}
}

func TestPromptSecret(t *testing.T) {
	_ = withStderr(t)
	setPassword(t, " 123456 ", nil)
	if got, err := PromptSecret("TOTP: "); err != nil || got != "123456" {
		t.Errorf("PromptSecret = %q, %v", got, err)
	}
	setPassword(t, "", errors.New("not a terminal"))
	if _, err := PromptSecret("TOTP: "); err == nil {
		t.Error("PromptSecret succeeded when the terminal read failed")
	}
}

func TestBrowserCommand(t *testing.T) {
	cases := map[string][]string{
		"darwin":  {"open", "https://x"},
		"windows": {"rundll32", "url.dll,FileProtocolHandler", "https://x"},
		"linux":   {"xdg-open", "https://x"},
	}
	for goos, want := range cases {
		name, args := browserCommand(goos, "https://x")
		if got := append([]string{name}, args...); !reflect.DeepEqual(got, want) {
			t.Errorf("browserCommand(%s) = %v, want %v", goos, got, want)
		}
	}
}

func TestOpenBrowser(t *testing.T) {
	prev := startProcess
	t.Cleanup(func() { startProcess = prev })

	var launched []string
	startProcess = func(name string, args ...string) error {
		launched = append([]string{name}, args...)
		return nil
	}
	if err := OpenBrowser("https://gw.example.com/ui"); err != nil {
		t.Fatalf("OpenBrowser: %v", err)
	}
	if len(launched) == 0 || launched[len(launched)-1] != "https://gw.example.com/ui" {
		t.Errorf("launched %v, want the URL as the last argument", launched)
	}

	startProcess = func(string, ...string) error { return exec.ErrNotFound }
	if err := OpenBrowser("https://x"); !errors.Is(err, exec.ErrNotFound) {
		t.Errorf("OpenBrowser = %v, want the launch error", err)
	}
}

// Fatalf and MustPrintJSON exit the process, so they run in a child test binary.
func TestFatalExits(t *testing.T) {
	switch os.Getenv("ORION_TEST_CLIFLAGS_EXIT") {
	case "fatalf":
		Fatalf("bad %s", "input")
		return
	case "mustjson":
		MustPrintJSON(map[string]interface{}{"f": func() {}})
		return
	}
	for mode, want := range map[string]string{"fatalf": "Error: bad input", "mustjson": "Error: json: unsupported type"} {
		t.Run(mode, func(t *testing.T) {
			cmd := exec.Command(os.Args[0], "-test.run=^TestFatalExits$")
			cmd.Env = append(os.Environ(), "ORION_TEST_CLIFLAGS_EXIT="+mode)
			var stderr bytes.Buffer
			cmd.Stderr = &stderr
			err := cmd.Run()
			var exitErr *exec.ExitError
			if !errors.As(err, &exitErr) || exitErr.ExitCode() != 1 {
				t.Fatalf("child exit = %v, want status 1", err)
			}
			if !strings.Contains(stderr.String(), want) {
				t.Errorf("stderr = %q, want %q", stderr.String(), want)
			}
		})
	}
}

func TestMustPrintJSON_WritesValue(t *testing.T) {
	var buf bytes.Buffer
	prev := Out
	Out = &buf
	t.Cleanup(func() { Out = prev })
	MustPrintJSON(map[string]int{"n": 1})
	if !strings.Contains(buf.String(), `"n": 1`) {
		t.Errorf("output = %q", buf.String())
	}
}

// --- logging -------------------------------------------------------------------

func TestLogger_VerboseReportsUnopenableLogFile(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "file")
	if err := os.WriteFile(file, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	stderr := withStderr(t)
	l := (&Common{Verbose: true, LogFile: filepath.Join(file, "sub", "x.log")}).Logger()
	l.Debug("detail")
	out := stderr()
	if !strings.Contains(out, "cannot open log file") {
		t.Errorf("stderr = %q, want the log file warning", out)
	}
	if !strings.Contains(out, "debug: detail") {
		t.Errorf("stderr = %q, want debug mirrored to the console in verbose mode", out)
	}
}

func TestLogFilePath_FallsBackToTempDirWithoutHome(t *testing.T) {
	t.Setenv("ORION_LOG_FILE", "")
	t.Setenv("HOME", "")
	if got := (&Common{}).LogFilePath("osh"); got != filepath.Join(os.TempDir(), "osh.log") {
		t.Errorf("LogFilePath = %q", got)
	}
}

func TestOpenLogFile_UncreatableDir(t *testing.T) {
	file := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(file, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := openLogFile(filepath.Join(file, "sub", "x.log")); err == nil {
		t.Error("openLogFile under a regular file succeeded")
	}
}

func TestConsoleHandler_AttrsGroupsAndInfoLevel(t *testing.T) {
	var buf bytes.Buffer
	var h slog.Handler = newConsoleHandler(&buf, "osh", slog.LevelDebug)
	h = h.WithAttrs([]slog.Attr{slog.String("session", "s-1")}).WithGroup("ignored")
	slog.New(h).Info("connected", "host", "web-01")
	if got := buf.String(); got != "osh: connected session=s-1 host=web-01\n" {
		t.Errorf("console line = %q", got)
	}
	if h.Enabled(context.Background(), slog.LevelDebug-1) {
		t.Error("handler enabled below its minimum level")
	}
}

// --- SDK client ------------------------------------------------------------------

func TestSDKClient_MFAPromptFlow(t *testing.T) {
	cases := []struct {
		name    string
		answer  string
		readErr error
		accept  bool
		wantErr string
	}{
		{"code accepted", "123456", nil, true, ""},
		{"code rejected", "000000", nil, false, "authentication failed"},
		{"empty code", "", nil, true, "mfa code required"},
		{"prompt fails", "", errors.New("no tty"), true, "mfa required"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_ = withStderr(t)
			setPassword(t, tc.answer, tc.readErr)
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/api/v1/public/auth/challenge":
					writeJSON(t, w, map[string]string{"challenge": "c"})
				case "/api/v1/public/login/key":
					var body map[string]any
					_ = jsonDecode(r, &body)
					if body["totp_code"] == nil || body["totp_code"] == "" {
						w.WriteHeader(http.StatusUnauthorized)
						_, _ = w.Write([]byte(`{"error":"mfa code required","mfa_required":true}`))
						return
					}
					if !tc.accept {
						w.WriteHeader(http.StatusUnauthorized)
						_, _ = w.Write([]byte(`{"error":"invalid code"}`))
						return
					}
					writeJSON(t, w, map[string]any{"api_key": "k"})
				}
			}))
			defer srv.Close()

			cfg := &common.Config{}
			cfg.Server.APIEndpoint = srv.URL
			cfg.Auth.KeyFile = writeTestKey(t, t.TempDir())
			cfg.Auth.User = "alice"
			client, err := (&Common{Timeout: 5 * time.Second}).SDKClient(cfg)
			if tc.wantErr == "" {
				if err != nil || client == nil {
					t.Fatalf("SDKClient = %v, %v; want a client", client, err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Errorf("SDKClient error = %v, want %q", err, tc.wantErr)
			}
		})
	}
}

func TestSDKClient_NonMFAFailureAndBadKey(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v1/public/auth/challenge" {
			writeJSON(t, w, map[string]string{"challenge": "c"})
			return
		}
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":"invalid credentials"}`))
	}))
	defer srv.Close()

	cfg := &common.Config{}
	cfg.Server.APIEndpoint = srv.URL
	cfg.Auth.KeyFile = writeTestKey(t, t.TempDir())
	cfg.Auth.User = "alice"
	if _, err := (&Common{Timeout: time.Second}).SDKClient(cfg); err == nil || !strings.Contains(err.Error(), "authentication failed") {
		t.Errorf("SDKClient = %v, want authentication failed", err)
	}

	bad := filepath.Join(t.TempDir(), "bad")
	if err := os.WriteFile(bad, []byte("garbage"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg.Auth.KeyFile = bad
	if _, err := (&Common{Timeout: time.Second}).SDKClient(cfg); err == nil || !strings.Contains(err.Error(), "failed to parse private key") {
		t.Errorf("SDKClient = %v, want a parse error", err)
	}

	cfg.Server.APIEndpoint = "://not a url"
	cfg.Auth.KeyFile = writeTestKey(t, t.TempDir())
	if _, err := (&Common{Timeout: time.Second}).SDKClient(cfg); err == nil {
		t.Error("SDKClient with an invalid endpoint succeeded")
	}
}

func TestAPIClient(t *testing.T) {
	setInteractive(t, false)
	if _, err := (&Common{ConfigFile: filepath.Join(t.TempDir(), "absent.yaml")}).APIClient(); err == nil {
		t.Error("APIClient without config succeeded")
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v1/public/auth/challenge" {
			writeJSON(t, w, map[string]string{"challenge": "c"})
			return
		}
		writeJSON(t, w, map[string]any{"api_key": "k"})
	}))
	defer srv.Close()
	dir := t.TempDir()
	path := filepath.Join(dir, "client.yaml")
	if err := common.SaveConfig(path, &common.Config{
		Server: common.ServerConfig{APIEndpoint: srv.URL},
		Auth:   common.AuthConfig{User: "alice", KeyFile: writeTestKey(t, dir)},
	}); err != nil {
		t.Fatal(err)
	}
	if c, err := (&Common{ConfigFile: path, Timeout: time.Second}).APIClient(); err != nil || c == nil {
		t.Errorf("APIClient = %v, %v", c, err)
	}
}

func bufioReader(s string) *bufio.Reader { return bufio.NewReader(strings.NewReader(s)) }

func jsonDecode(r *http.Request, v any) error { return json.NewDecoder(r.Body).Decode(v) }
