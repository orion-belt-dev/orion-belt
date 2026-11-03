package api

import (
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/orion-belt-dev/orion-belt/packaging/keys"
)

func TestBuildAgentInstallScript_VerifiesEveryDownload(t *testing.T) {
	for _, osID := range []string{"debian", "rhel", "suse", "alpine", "arch", "linux"} {
		script := buildAgentInstallScript(osID, "a1", "gw", 2222, "http://pkg", "1.4.0", "KEY", "", "", nil, false)

		if !strings.Contains(script, `ORION_RELEASE_FPR="`+keys.ReleaseFingerprint()+`"`) {
			t.Errorf("%s: release fingerprint not pinned", osID)
		}
		if !strings.Contains(script, strings.TrimSpace(keys.ReleaseKey())) {
			t.Errorf("%s: release key not embedded", osID)
		}
		// Every download goes through fetch_verified into the private $WORK.
		if strings.Contains(script, "fetch_first") || strings.Contains(script, "/tmp/orion-belt-agent") {
			t.Errorf("%s: unverified or shared-/tmp download left in the script", osID)
		}
		if strings.Contains(script, `"$PKG_BASE/orion-belt-agent";`) || strings.Contains(script, `"$PKG_BASE/orion-belt-agent"`+"\n") {
			t.Errorf("%s: bare binary fallback cannot be verified", osID)
		}
		if !strings.Contains(script, "WORK=$(mktemp -d)") {
			t.Errorf("%s: no private work directory", osID)
		}
		installs := strings.Count(script, "fetch_verified \"$")
		if installs < 1 {
			t.Errorf("%s: no verified download", osID)
		}
	}
}

func TestBuildAgentInstallScript_UnsignedBuildIsExplicit(t *testing.T) {
	signed := buildAgentInstallScript("suse", "a1", "gw", 2222, "http://pkg", "1.4.0", "KEY", "", "", nil, false)
	if !strings.Contains(signed, "ORION_ALLOW_UNSIGNED_BUILD=0") || strings.Contains(signed, "ZYPPER_UNSIGNED=--allow-unsigned-rpm") {
		t.Error("default script accepts unsigned packages")
	}
	unsigned := buildAgentInstallScript("suse", "a1", "gw", 2222, "http://pkg", "1.4.0", "KEY", "", "", nil, true)
	for _, want := range []string{"ORION_ALLOW_UNSIGNED_BUILD=1", "ZYPPER_UNSIGNED=--allow-unsigned-rpm", "WARNING: this installer accepts unsigned", `zypper -n install $ZYPPER_UNSIGNED`} {
		if !strings.Contains(unsigned, want) {
			t.Errorf("unsigned build script missing %q", want)
		}
	}
}

func TestReleaseKeyMatchesFingerprintFile(t *testing.T) {
	fpr := keys.ReleaseFingerprint()
	if len(fpr) != 40 || strings.Trim(fpr, "0123456789ABCDEF") != "" {
		t.Fatalf("fingerprint = %q, want 40 upper-case hex digits", fpr)
	}
	gpg, err := exec.LookPath("gpg")
	if err != nil {
		t.Skip("no gpg")
	}
	home := t.TempDir()
	cmd := exec.Command(gpg, "--batch", "--with-colons", "--show-keys")
	cmd.Stdin = strings.NewReader(keys.ReleaseKey())
	cmd.Env = append(os.Environ(), "GNUPGHOME="+home)
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("gpg --show-keys: %v", err)
	}
	// The first fpr record is the primary key's.
	for _, line := range strings.Split(string(out), "\n") {
		if f := strings.Split(line, ":"); len(f) > 9 && f[0] == "fpr" {
			if f[9] != fpr {
				t.Errorf("orion-belt.asc primary key is %s, orion-belt.fingerprint says %s", f[9], fpr)
			}
			return
		}
	}
	t.Fatal("no fingerprint in orion-belt.asc")
}

// verifyHarness runs the script's verification functions under bash against
// a local package base, with a throwaway key standing in for the release key.
type verifyHarness struct {
	t        *testing.T
	bash     string
	gnupg    string // keyring holding the test signing keys
	work     string
	files    map[string][]byte // served by the package base
	srv      *httptest.Server
	trusted  string // fingerprint the script pins
	pubKey   string
}

func newVerifyHarness(t *testing.T) *verifyHarness {
	t.Helper()
	bash, err := exec.LookPath("bash")
	if err != nil {
		t.Skip("no bash")
	}
	if _, err := exec.LookPath("gpg"); err != nil {
		t.Skip("no gpg")
	}
	for _, tool := range []string{"curl", "sha256sum", "awk"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skip("no " + tool)
		}
	}
	h := &verifyHarness{t: t, bash: bash, gnupg: t.TempDir(), work: t.TempDir(), files: map[string][]byte{}}
	h.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, ok := h.files[strings.TrimPrefix(r.URL.Path, "/")]
		if !ok {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write(b)
	}))
	t.Cleanup(h.srv.Close)
	h.trusted = h.genKey("Release <release@example.com>")
	h.pubKey = string(h.gpg("", "--armor", "--export", h.trusted))
	return h
}

func (h *verifyHarness) gpg(stdin string, args ...string) []byte {
	h.t.Helper()
	cmd := exec.Command("gpg", append([]string{"--batch", "--quiet", "--pinentry-mode", "loopback", "--passphrase", ""}, args...)...)
	cmd.Env = append(os.Environ(), "GNUPGHOME="+h.gnupg)
	if stdin != "" {
		cmd.Stdin = strings.NewReader(stdin)
	}
	out, err := cmd.Output()
	if err != nil {
		h.t.Fatalf("gpg %v: %v", args, err)
	}
	return out
}

// genKey creates a signing key and returns its fingerprint.
func (h *verifyHarness) genKey(uid string) string {
	h.t.Helper()
	h.gpg("", "--quick-gen-key", uid, "ed25519", "sign", "never")
	out := h.gpg("", "--with-colons", "--fingerprint", uid)
	for _, line := range strings.Split(string(out), "\n") {
		if f := strings.Split(line, ":"); len(f) > 9 && f[0] == "fpr" {
			return f[9]
		}
	}
	h.t.Fatalf("no fingerprint for %s", uid)
	return ""
}

// publish serves checksums.txt for files, signed by signer.
func (h *verifyHarness) publish(signer string, files map[string][]byte) {
	h.t.Helper()
	var sums strings.Builder
	for name, b := range files {
		d := sha256.Sum256(b)
		sums.WriteString(hex.EncodeToString(d[:]) + "  " + name + "\n")
	}
	path := filepath.Join(h.t.TempDir(), "checksums.txt")
	if err := os.WriteFile(path, []byte(sums.String()), 0o600); err != nil {
		h.t.Fatal(err)
	}
	h.gpg("", "--local-user", signer, "--output", path+".sig", "--detach-sign", path)
	sig, err := os.ReadFile(path + ".sig")
	if err != nil {
		h.t.Fatal(err)
	}
	h.files["checksums.txt"] = []byte(sums.String())
	h.files["checksums.txt.sig"] = sig
}

// run executes body after the verification functions; RELEASE points at a
// base that serves nothing, so only the package base can satisfy them.
func (h *verifyHarness) run(body string) (string, error) {
	h.t.Helper()
	return h.runWith(false, body)
}

// runWith is run for a script generated with allow_unsigned_build or not.
func (h *verifyHarness) runWith(allowUnsigned bool, body string) (string, error) {
	h.t.Helper()
	flag := "0"
	if allowUnsigned {
		flag = "1"
	}
	if err := os.WriteFile(filepath.Join(h.work, "orion-belt.asc"), []byte(h.pubKey), 0o600); err != nil {
		h.t.Fatal(err)
	}
	_ = os.Remove(filepath.Join(h.work, "checksums.ok"))
	_ = os.Remove(filepath.Join(h.work, "checksums.none"))
	script := "set -euo pipefail\n" +
		"WORK=" + h.work + "\n" +
		"PKG_BASE=" + h.srv.URL + "\n" +
		"RELEASE=" + h.srv.URL + "/missing\n" +
		"VERSION=1.4.0\n" +
		"ORION_RELEASE_FPR=" + h.trusted + "\n" +
		"ORION_ALLOW_UNSIGNED_BUILD=" + flag + "\n" +
		agentInstallVerify + body
	cmd := exec.Command(h.bash, "-c", script)
	out, err := cmd.CombinedOutput()
	return string(out), err
}

func TestAgentInstallVerify(t *testing.T) {
	h := newVerifyHarness(t)
	good := []byte("genuine agent package")
	const name = "orion-belt-agent_1.4.0_x86_64.apk"

	t.Run("signed checksum match is accepted", func(t *testing.T) {
		h.publish(h.trusted, map[string][]byte{name: good})
		h.files["apk/x86_64/orion-belt-agent-1.4.0.apk"] = good
		out, err := h.run(`fetch_verified "$WORK/pkg" ` + name + ` "$PKG_BASE/apk/x86_64/orion-belt-agent-1.4.0.apk" && cat "$WORK/pkg"`)
		if err != nil || !strings.Contains(out, string(good)) || !strings.Contains(out, "Release checksums verified") {
			t.Errorf("err=%v out=%s", err, out)
		}
	})

	t.Run("tampered package is skipped for the next candidate", func(t *testing.T) {
		h.publish(h.trusted, map[string][]byte{name: good})
		h.files["mirror/"+name] = []byte("backdoored agent package")
		h.files[name] = good
		out, err := h.run(`fetch_verified "$WORK/pkg" ` + name + ` "$PKG_BASE/mirror/` + name + `" "$PKG_BASE/` + name + `" && cat "$WORK/pkg"`)
		if err != nil || !strings.Contains(out, "does not match the signed checksum") || !strings.HasSuffix(out, string(good)) {
			t.Errorf("err=%v out=%s", err, out)
		}
	})

	t.Run("tampered package alone is refused", func(t *testing.T) {
		h.publish(h.trusted, map[string][]byte{name: good})
		h.files["mirror/"+name] = []byte("backdoored agent package")
		out, err := h.run(`if fetch_verified "$WORK/pkg" ` + name + ` "$PKG_BASE/mirror/` + name + `"; then echo INSTALLED; else echo REFUSED; fi; test ! -e "$WORK/pkg" && echo CLEAN`)
		if err != nil || !strings.Contains(out, "REFUSED") || !strings.Contains(out, "CLEAN") {
			t.Errorf("err=%v out=%s", err, out)
		}
	})

	t.Run("file missing from checksums is refused", func(t *testing.T) {
		h.publish(h.trusted, map[string][]byte{name: good})
		h.files["other.apk"] = good
		out, err := h.run(`if fetch_verified "$WORK/pkg" other.apk "$PKG_BASE/other.apk"; then echo INSTALLED; else echo REFUSED; fi`)
		if err != nil || !strings.Contains(out, "REFUSED") || !strings.Contains(out, "not in the release checksums") {
			t.Errorf("err=%v out=%s", err, out)
		}
	})

	t.Run("checksums signed by another key stop the install", func(t *testing.T) {
		rogue := h.genKey("Rogue <rogue@example.com>")
		evil := []byte("backdoored agent package")
		h.publish(rogue, map[string][]byte{name: evil})
		h.files[name] = evil
		out, err := h.run(`fetch_verified "$WORK/pkg" ` + name + ` "$PKG_BASE/` + name + `"; echo INSTALLED`)
		if err == nil || strings.Contains(out, "INSTALLED") || !strings.Contains(out, "not signed by the release key") {
			t.Errorf("err=%v out=%s", err, out)
		}
	})

	t.Run("unsigned checksums stop the install", func(t *testing.T) {
		h.publish(h.trusted, map[string][]byte{name: good})
		h.files["checksums.txt"] = append(h.files["checksums.txt"], []byte("0000  extra\n")...)
		h.files[name] = good
		out, err := h.run(`fetch_verified "$WORK/pkg" ` + name + ` "$PKG_BASE/` + name + `"; echo INSTALLED`)
		if err == nil || strings.Contains(out, "INSTALLED") {
			t.Errorf("err=%v out=%s", err, out)
		}
	})

	// A script generated with allow_unsigned_build, for a local snapshot.
	t.Run("unsigned build: signed checksums are still enforced", func(t *testing.T) {
		h.publish(h.trusted, map[string][]byte{name: good})
		h.files[name] = []byte("backdoored agent package")
		out, err := h.runWith(true, `if fetch_verified "$WORK/pkg" `+name+` "$PKG_BASE/`+name+`"; then echo INSTALLED; else echo REFUSED; fi`)
		if err != nil || !strings.Contains(out, "REFUSED") {
			t.Errorf("err=%v out=%s", err, out)
		}
	})

	t.Run("unsigned build: plain checksums catch a corrupt download", func(t *testing.T) {
		rogue := h.genKey("Snapshot <snapshot@example.com>")
		h.publish(rogue, map[string][]byte{name: good})
		h.files[name] = []byte("truncated")
		out, err := h.runWith(true, `if fetch_verified "$WORK/pkg" `+name+` "$PKG_BASE/`+name+`"; then echo INSTALLED; else echo REFUSED; fi`)
		if err != nil || !strings.Contains(out, "REFUSED") || !strings.Contains(out, "UNSIGNED checksums.txt") {
			t.Errorf("err=%v out=%s", err, out)
		}
		h.files[name] = good
		out, err = h.runWith(true, `fetch_verified "$WORK/pkg" `+name+` "$PKG_BASE/`+name+`" && cat "$WORK/pkg"`)
		if err != nil || !strings.HasSuffix(out, string(good)) {
			t.Errorf("err=%v out=%s", err, out)
		}
	})

	t.Run("unsigned build without checksums installs with a warning", func(t *testing.T) {
		delete(h.files, "checksums.txt")
		delete(h.files, "checksums.txt.sig")
		h.files[name] = good
		out, err := h.runWith(true, `fetch_verified "$WORK/pkg" `+name+` "$PKG_BASE/`+name+`" && cat "$WORK/pkg"`)
		if err != nil || !strings.Contains(out, "WITHOUT any verification") || !strings.HasSuffix(out, string(good)) {
			t.Errorf("err=%v out=%s", err, out)
		}
		// The same mirror is refused by a normal script.
		if out, err := h.run(`fetch_verified "$WORK/pkg" ` + name + ` "$PKG_BASE/` + name + `"; echo INSTALLED`); err == nil || strings.Contains(out, "INSTALLED") {
			t.Errorf("default script accepted a mirror without checksums: %s", out)
		}
	})
}

// scripts/install-server.sh carries its own copy of the verification code
// (it is fetched and run on its own). It must stay identical to the agent
// script's, which the tests above exercise, and pin the same key.
func TestInstallServerScriptSharesVerification(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "..", "scripts", "install-server.sh"))
	if err != nil {
		t.Fatal(err)
	}
	script := string(raw)
	const begin, end = "# BEGIN release-verify\n", "# END release-verify\n"
	i, j := strings.Index(script, begin), strings.Index(script, end)
	if i < 0 || j < i {
		t.Fatal("release-verify markers missing from install-server.sh")
	}
	if got := script[i+len(begin) : j]; got != agentInstallVerify {
		t.Error("install-server.sh release-verify block differs from agentInstallVerify; copy it over")
	}
	if !strings.Contains(script, `ORION_RELEASE_FPR="`+keys.ReleaseFingerprint()+`"`) {
		t.Error("install-server.sh does not pin the release fingerprint")
	}
	if !strings.Contains(script, strings.TrimSpace(keys.ReleaseKey())) {
		t.Error("install-server.sh does not embed packaging/keys/orion-belt.asc")
	}
	// Nothing is downloaded outside the verified path.
	for n, line := range strings.Split(script[:i]+script[j:], "\n") {
		if strings.Contains(line, "curl -fsSL -o") {
			t.Errorf("install-server.sh line %d downloads outside fetch_verified: %s", n+1, strings.TrimSpace(line))
		}
	}
	if bash, err := exec.LookPath("bash"); err == nil {
		if out, err := exec.Command(bash, "-n", filepath.Join("..", "..", "scripts", "install-server.sh")).CombinedOutput(); err != nil {
			t.Errorf("bash -n: %v\n%s", err, out)
		}
	}
}
