package cliflags

import (
	"fmt"
	"os/exec"
	"runtime"
)

// startProcess launches a command without waiting for it. A variable so tests
// can check what would be opened without starting a real browser.
var startProcess = func(name string, args ...string) error {
	return exec.Command(name, args...).Start()
}

// OpenBrowser opens url in the user’s default browser.
func OpenBrowser(url string) error {
	name, args := browserCommand(runtime.GOOS, url)
	if err := startProcess(name, args...); err != nil {
		return fmt.Errorf("open browser: %w", err)
	}
	return nil
}

// browserCommand picks the platform's URL opener.
func browserCommand(goos, url string) (string, []string) {
	switch goos {
	case "darwin":
		return "open", []string{url}
	case "windows":
		return "rundll32", []string{"url.dll,FileProtocolHandler", url}
	default:
		return "xdg-open", []string{url}
	}
}
