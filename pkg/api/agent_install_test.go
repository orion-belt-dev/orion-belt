package api

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestBuildAgentInstallScriptDebian(t *testing.T) {
	script := buildAgentInstallScript(
		"debian",
		"web-01",
		"gw.example.com",
		2222,
		"https://packages.example.com",
		"1.2.3",
		"-----BEGIN PRIVATE KEY-----\nABC\n-----END PRIVATE KEY-----",
		"",
		"",
		map[string]string{"os": "debian", "env": "lab"},
	)
	checks := []string{
		"#!/usr/bin/env bash",
		`host: "gw.example.com"`,
		`name: "web-01"`,
		"orion-belt-agent_${DEB_V}_${DEB_ARCH}.deb",
		"ORION_AGENT_KEY",
		"-----BEGIN PRIVATE KEY-----",
		"systemctl enable --now orion-belt-agent",
		`PKG_BASE="https://packages.example.com"`,
	}
	for _, c := range checks {
		if !strings.Contains(script, c) {
			t.Fatalf("script missing %q\n%s", c, script)
		}
	}
}

func TestBuildAgentInstallScriptAlpine(t *testing.T) {
	script := buildAgentInstallScript("alpine", "a1", "10.0.0.1", 2222, "http://pkg", "0.4.0", "KEYDATA", "", "", nil)
	if !strings.Contains(script, "apk add --allow-untrusted") {
		t.Fatal("expected apk install path")
	}
	if !strings.Contains(script, "$PKG_BASE/apk/${APK_ARCH}/orion-belt-agent-${APK_V}.apk") {
		t.Fatal("expected apk package name")
	}
}

func TestBuildAgentInstallScriptWithHostCert(t *testing.T) {
	script := buildAgentInstallScript(
		"debian", "web-01", "gw.example.com", 2222, "https://pkg", "1.0.0",
		"-----BEGIN PRIVATE KEY-----\nABC\n-----END PRIVATE KEY-----",
		"ssh-ed25519-cert-v01@openssh.com AAAA cert-comment",
		"ssh-ed25519 AAAA host-ca",
		nil,
	)
	for _, c := range []string{
		"ORION_AGENT_CERT",
		"agent_key-cert.pub",
		"host_ca_public_key:",
		"ssh-ed25519-cert-v01@openssh.com",
	} {
		if !strings.Contains(script, c) {
			t.Fatalf("script missing %q", c)
		}
	}
}

func TestBuildAgentInstallScriptArch(t *testing.T) {
	script := buildAgentInstallScript("arch", "a1", "10.0.0.1", 2222, "http://pkg", "1.4.0", "KEYDATA", "", "", nil)
	for _, c := range []string{
		"pacman -U --noconfirm",
		"$PKG_BASE/archlinux/${PAC_ARCH}/orion-belt-agent-${PAC_V}-1-${PAC_ARCH}.pkg.tar.zst",
		`"$svc" setup`,
		"svc=/usr/lib/orion-belt/orion-belt-agent-service",
	} {
		if !strings.Contains(script, c) {
			t.Fatalf("script missing %q", c)
		}
	}
}

// TestAgentInstallArchMapsEveryReleaseArchitecture runs the script's
// architecture block under sh with uname stubbed, for every CPU the release
// builds packages for.
func TestAgentInstallArchMapsEveryReleaseArchitecture(t *testing.T) {
	sh, err := exec.LookPath("sh")
	if err != nil {
		t.Skip("no sh")
	}
	cases := map[string]string{
		"x86_64":      "amd64 amd64 x86_64 x86_64 x86_64",
		"aarch64":     "arm64 arm64 aarch64 aarch64 aarch64",
		"armv7l":      "armv7 armhf armv7hl armv7 armv7h",
		"armv6l":      "armv6 armhf armv6hl armhf armv6h",
		"armv5tel":    "armv5 armel armv5tel armel armv5",
		"i686":        "386 i386 i386 x86 i686",
		"ppc64le":     "ppc64le ppc64el ppc64le ppc64le ppc64le",
		"s390x":       "s390x s390x s390x s390x s390x",
		"riscv64":     "riscv64 riscv64 riscv64 riscv64 riscv64",
		"loongarch64": "loong64 loong64 loongarch64 loongarch64 loong64",
	}
	sed, err := exec.LookPath("sed")
	if err != nil {
		t.Skip("no sed")
	}
	for machine, want := range cases {
		bin := t.TempDir()
		stub := "#!/bin/sh\necho " + machine + "\n"
		if err := os.WriteFile(filepath.Join(bin, "uname"), []byte(stub), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(sed, filepath.Join(bin, "sed")); err != nil {
			t.Fatal(err)
		}
		cmd := exec.Command(sh, "-c", agentInstallArch+`echo "$GO_ARCH $DEB_ARCH $RPM_ARCH $APK_ARCH $PAC_ARCH"`)
		// Only the stub and sed on PATH: no dpkg, so the Debian armhf
		// override is off.
		cmd.Env = []string{"PATH=" + bin}
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("%s: %v\n%s", machine, err, out)
		}
		if got := strings.TrimSpace(string(out)); got != want {
			t.Errorf("%s: got %q, want %q", machine, got, want)
		}
	}

	bin := t.TempDir()
	if err := os.WriteFile(filepath.Join(bin, "uname"), []byte("#!/bin/sh\necho mips\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(sed, filepath.Join(bin, "sed")); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(sh, "-c", agentInstallArch)
	cmd.Env = []string{"PATH=" + bin}
	if out, err := cmd.CombinedOutput(); err == nil || !strings.Contains(string(out), "unsupported CPU: mips") {
		t.Errorf("unknown CPU: err=%v out=%s", err, out)
	}
}

// TestAgentInstallPackageVersions checks the script spells versions the way
// nfpm names each package format, for a release and a pre-release.
func TestAgentInstallPackageVersions(t *testing.T) {
	sh, err := exec.LookPath("sh")
	if err != nil {
		t.Skip("no sh")
	}
	cases := map[string]string{
		"1.4.0":                  "1.4.0 1.4.0 1.4.0 1.4.0",
		"1.4.0-rc.1":             "1.4.0~rc.1 1.4.0~rc.1 1.4.0_rc.1 1.4.0rc.1",
		"1.3.0-SNAPSHOT-73ed19e": "1.3.0~SNAPSHOT-73ed19e 1.3.0~SNAPSHOT_73ed19e 1.3.0_SNAPSHOT-73ed19e 1.3.0SNAPSHOT_73ed19e",
	}
	for v, want := range cases {
		cmd := exec.Command(sh, "-c", "VERSION="+v+"\n"+agentInstallArch+`echo "$DEB_V $RPM_V $APK_V $PAC_V"`)
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("%s: %v\n%s", v, err, out)
		}
		if got := strings.TrimSpace(string(out)); got != want {
			t.Errorf("%s: got %q, want %q", v, got, want)
		}
	}
}
