package cve_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// Same pin as scripts/cve-check.sh. A govulncheck built by an older compiler
// cannot load a module that requires this toolchain.
const govulncheckVersion = "v1.9.0"

// TestZeroCVEs runs govulncheck against the module. Intended for CI / make cve.
func TestZeroCVEs(t *testing.T) {
	if os.Getenv("ORION_CVE_E2E") == "" && os.Getenv("CI") == "" {
		t.Skip("set ORION_CVE_E2E=1 or CI=1 to run CVE e2e gate")
	}

	root := moduleRoot(t)
	// Read the toolchain from go.mod. A hardcoded older GOTOOLCHAIN makes
	// govulncheck refuse to load the module ("requires go >= …").
	toolchain := "go" + goDirective(t, root)
	env := withToolchain(toolchain)

	gopath := os.Getenv("GOPATH")
	if gopath == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			t.Fatal(err)
		}
		gopath = filepath.Join(home, "go")
	}
	bin := filepath.Join(gopath, "bin", "govulncheck")

	install := exec.Command("go", "install", "golang.org/x/vuln/cmd/govulncheck@"+govulncheckVersion)
	install.Dir = root
	install.Env = env
	if out, err := install.CombinedOutput(); err != nil {
		t.Fatalf("install govulncheck: %v\n%s", err, out)
	}

	cmd := exec.Command(bin, "./...")
	cmd.Dir = root
	cmd.Env = env
	out, err := cmd.CombinedOutput()
	t.Logf("%s", out)
	if err != nil {
		t.Fatalf("govulncheck reported vulnerabilities: %v", err)
	}
}

func goDirective(t *testing.T, root string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(root, "go.mod"))
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range strings.Split(string(data), "\n") {
		fields := strings.Fields(line)
		if len(fields) >= 2 && fields[0] == "go" {
			return fields[1]
		}
	}
	t.Fatal("go directive not found in go.mod")
	return ""
}

// withToolchain replaces any inherited GOTOOLCHAIN. Appending a second copy
// does not override the first: getenv returns the earliest match.
func withToolchain(toolchain string) []string {
	const prefix = "GOTOOLCHAIN="
	env := os.Environ()
	out := make([]string, 0, len(env)+1)
	for _, entry := range env {
		if strings.HasPrefix(entry, prefix) {
			continue
		}
		out = append(out, entry)
	}
	return append(out, prefix+toolchain)
}

func moduleRoot(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	dir := filepath.Dir(file)
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("go.mod not found")
		}
		dir = parent
	}
}
