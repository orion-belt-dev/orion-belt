// Package keys carries the public key that signs Orion Belt releases, so the
// server can pin it in the agent install scripts it generates. The files next
// to this one are the source; rotating the key means replacing them.
package keys

import (
	_ "embed"
	"strings"
)

//go:embed orion-belt.asc
var releaseKey string

//go:embed orion-belt.fingerprint
var releaseFingerprint string

// ReleaseKey returns the ASCII-armoured release signing key.
func ReleaseKey() string { return releaseKey }

// ReleaseFingerprint returns the primary fingerprint of the release key, as
// upper-case hex without spaces (the form gpg prints in VALIDSIG).
func ReleaseFingerprint() string {
	return strings.ToUpper(strings.ReplaceAll(strings.TrimSpace(releaseFingerprint), " ", ""))
}
