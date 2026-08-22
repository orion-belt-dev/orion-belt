package cve_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
)

// TestZeroCVEs runs govulncheck against the module. Intended for CI / make cve.
func TestZeroCVEs(t *testing.T) {
	if os.Getenv("ORION_CVE_E2E") == "" && os.Getenv("CI") == "" {
		t.Skip("set ORION_CVE_E2E=1 or CI=1 to run CVE e2e gate")
	}

	root := moduleRoot(t)
	govulncheck, err := exec.LookPath("govulncheck")
	if err != nil {
		install := exec.Command("go", "install", "golang.org/x/vuln/cmd/govulncheck@latest")
		install.Dir = root
		install.Env = append(os.Environ(), "GOTOOLCHAIN=go1.26.6")
		if out, err := install.CombinedOutput(); err != nil {
			t.Fatalf("install govulncheck: %v\n%s", err, out)
		}
		gopath := os.Getenv("GOPATH")
		if gopath == "" {
			home, _ := os.UserHomeDir()
			gopath = filepath.Join(home, "go")
		}
		govulncheck = filepath.Join(gopath, "bin", "govulncheck")
	}

	cmd := exec.Command(govulncheck, "./...")
	cmd.Dir = root
	cmd.Env = append(os.Environ(), "GOTOOLCHAIN=go1.26.6")
	out, err := cmd.CombinedOutput()
	t.Logf("%s", out)
	if err != nil {
		t.Fatalf("govulncheck reported vulnerabilities: %v", err)
	}
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
