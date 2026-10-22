package cliflags

import (
	"bufio"
	"fmt"
	"os"
	"strings"
	"syscall"

	"golang.org/x/term"
)

// PromptLine reads a single line from stdin (echo on).
func PromptLine(label string) (string, error) {
	fmt.Fprint(os.Stderr, label)
	s, err := bufio.NewReader(os.Stdin).ReadString('\n')
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(s), nil
}

// readPassword reads stdin without echo. A variable so tests can answer the
// prompt without a terminal.
var readPassword = func() ([]byte, error) { return term.ReadPassword(int(syscall.Stdin)) }

// PromptSecret reads a line without echo (passwords / TOTP).
func PromptSecret(label string) (string, error) {
	fmt.Fprint(os.Stderr, label)
	b, err := readPassword()
	fmt.Fprintln(os.Stderr)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(b)), nil
}
