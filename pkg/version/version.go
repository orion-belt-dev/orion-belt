// Package version holds build-time identity for Orion Belt binaries.
package version

import "fmt"

// Set via -ldflags "-X github.com/orion-belt-dev/orion-belt/pkg/version.Version=..."
var (
	Version = "dev"
	Commit  = "none"
	Date    = "unknown"
)

// String returns a short human-readable build id.
func String() string {
	if Commit == "" || Commit == "none" {
		return Version
	}
	short := Commit
	if len(short) > 8 {
		short = short[:8]
	}
	return fmt.Sprintf("%s+%s", Version, short)
}

// Info is JSON-friendly build metadata.
func Info() map[string]string {
	return map[string]string{
		"version": Version,
		"commit":  Commit,
		"date":    Date,
		"display": String(),
	}
}
