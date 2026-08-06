package cliflags

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
	"text/tabwriter"
	"time"
)

// Out is where command output goes. Tests swap it for a buffer.
var Out io.Writer = os.Stdout

// PrintJSON writes v as indented JSON. A nil slice prints as `[]` rather than
// `null` so shell pipelines (jq, etc.) always get a value of the shape the
// table view implies.
func PrintJSON(v interface{}) error {
	if v == nil {
		v = struct{}{}
	}
	enc := json.NewEncoder(Out)
	enc.SetIndent("", "  ")
	enc.SetEscapeHTML(false)
	return enc.Encode(v)
}

// MustPrintJSON writes v as JSON, exiting with a readable message if the value
// cannot be encoded. Commands print JSON as their last act, so there is nothing
// useful to recover to.
func MustPrintJSON(v interface{}) {
	if err := PrintJSON(v); err != nil {
		Fatalf("%v", err)
	}
}

// YesNo renders a boolean for table output.
func YesNo(b bool) string {
	if b {
		return "yes"
	}
	return "no"
}

// OrDash renders an empty string as "-" so columns stay aligned.
func OrDash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

// Table renders aligned columns with a dashed rule under the header, matching
// the layout `oadmin requests list` has always used.
type Table struct {
	w    *tabwriter.Writer
	cols int
}

// NewTable starts a table with the given column headers.
func NewTable(headers ...string) *Table {
	t := &Table{w: tabwriter.NewWriter(Out, 0, 0, 2, ' ', 0), cols: len(headers)}
	fmt.Fprintln(t.w, strings.Join(headers, "\t"))
	rule := make([]string, len(headers))
	for i, h := range headers {
		rule[i] = strings.Repeat("-", len(h))
	}
	fmt.Fprintln(t.w, strings.Join(rule, "\t"))
	return t
}

// Row appends one record. Values are rendered with %v.
func (t *Table) Row(values ...interface{}) {
	cells := make([]string, len(values))
	for i, v := range values {
		cells[i] = fmt.Sprintf("%v", v)
	}
	fmt.Fprintln(t.w, strings.Join(cells, "\t"))
}

// Flush writes the table out.
func (t *Table) Flush() { _ = t.w.Flush() }

// Print writes a plain line to the command output stream.
func Print(format string, args ...interface{}) {
	fmt.Fprintf(Out, format+"\n", args...)
}

// Fatalf reports a user-facing failure and exits non-zero. Errors go to stderr
// so `--json` output on stdout stays parseable.
func Fatalf(format string, args ...interface{}) {
	fmt.Fprintf(os.Stderr, "Error: "+format+"\n", args...)
	os.Exit(1)
}

// Short trims an identifier for table display. Full IDs remain available via
// --json, so tables stay readable without losing access to the real value.
func Short(id string) string {
	if len(id) <= 12 {
		return id
	}
	return id[:8] + "…"
}

// Truncate shortens s to maxLen characters, ellipsizing the tail.
func Truncate(s string, maxLen int) string {
	if maxLen <= 3 || len(s) <= maxLen {
		return s
	}
	return s[:maxLen-3] + "..."
}

// FormatTime renders a timestamp for table output; zero times render as "-".
func FormatTime(t time.Time) string {
	if t.IsZero() {
		return "-"
	}
	return t.Format(time.RFC3339)
}

// FormatTimePtr renders an optional timestamp, printing "-" when unset.
func FormatTimePtr(t *time.Time) string {
	if t == nil {
		return "-"
	}
	return FormatTime(*t)
}

// FormatDuration renders a duration at single-unit precision (45s, 12m, 3h, 2d).
func FormatDuration(d time.Duration) string {
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%ds", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh", int(d.Hours()))
	default:
		return fmt.Sprintf("%dd", int(d.Hours()/24))
	}
}

// FormatTags renders a machine tag map as sorted k=v pairs so repeated runs
// produce identical output (Go map iteration order is randomized).
func FormatTags(tags map[string]string) string {
	if len(tags) == 0 {
		return "-"
	}
	keys := make([]string, 0, len(tags))
	for k := range tags {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	pairs := make([]string, 0, len(keys))
	for _, k := range keys {
		pairs = append(pairs, k+"="+tags[k])
	}
	return strings.Join(pairs, ",")
}

// ParseKeyValues parses repeated `key=value` flag values into a map.
func ParseKeyValues(values []string) (map[string]string, error) {
	if len(values) == 0 {
		return nil, nil
	}
	out := make(map[string]string, len(values))
	for _, v := range values {
		k, val, ok := strings.Cut(v, "=")
		if !ok || strings.TrimSpace(k) == "" {
			return nil, fmt.Errorf("invalid key=value pair %q", v)
		}
		out[strings.TrimSpace(k)] = val
	}
	return out, nil
}
