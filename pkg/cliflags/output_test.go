package cliflags

import (
	"bytes"
	"strings"
	"testing"
	"time"
)

func captureOut(t *testing.T, fn func()) string {
	t.Helper()
	var buf bytes.Buffer
	previous := Out
	Out = &buf
	defer func() { Out = previous }()
	fn()
	return buf.String()
}

func TestPrintJSONIndentsAndDoesNotEscapeHTML(t *testing.T) {
	out := captureOut(t, func() {
		if err := PrintJSON(map[string]string{"reason": "a&b <prod>"}); err != nil {
			t.Fatalf("PrintJSON: %v", err)
		}
	})

	if !strings.Contains(out, `"reason": "a&b <prod>"`) {
		t.Errorf("expected unescaped, indented JSON, got %q", out)
	}
}

func TestPrintJSONRendersNilAsAValue(t *testing.T) {
	out := captureOut(t, func() {
		if err := PrintJSON(nil); err != nil {
			t.Fatalf("PrintJSON: %v", err)
		}
	})

	if strings.TrimSpace(out) != "{}" {
		t.Errorf("nil should print as an empty object so pipelines get a value, got %q", out)
	}
}

func TestTableAlignsColumnsUnderARule(t *testing.T) {
	out := captureOut(t, func() {
		table := NewTable("NAME", "PORT")
		table.Row("web-01", 22)
		table.Row("database-primary", 2222)
		table.Flush()
	})

	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	if len(lines) != 4 {
		t.Fatalf("want header, rule, and 2 rows; got %d lines: %q", len(lines), out)
	}
	if !strings.HasPrefix(lines[1], "----") {
		t.Errorf("second line should be the header rule, got %q", lines[1])
	}

	// tabwriter pads every row to the same column offset.
	first := strings.Index(lines[2], "22")
	second := strings.Index(lines[3], "2222")
	if first != second {
		t.Errorf("columns are not aligned: %q vs %q", lines[2], lines[3])
	}
}

func TestShortKeepsIdentifiersReadable(t *testing.T) {
	if got := Short("short-id"); got != "short-id" {
		t.Errorf("short values should pass through, got %q", got)
	}
	got := Short("11111111-2222-3333-4444-555555555555")
	if got != "11111111…" {
		t.Errorf("Short(uuid) = %q", got)
	}
}

func TestTruncate(t *testing.T) {
	cases := []struct {
		in     string
		maxLen int
		want   string
	}{
		{"short", 10, "short"},
		{"exactly-10", 10, "exactly-10"},
		{"a much longer reason", 10, "a much ..."},
		{"anything", 2, "anything"}, // no room for an ellipsis: leave it alone
	}
	for _, tc := range cases {
		if got := Truncate(tc.in, tc.maxLen); got != tc.want {
			t.Errorf("Truncate(%q, %d) = %q, want %q", tc.in, tc.maxLen, got, tc.want)
		}
	}
}

func TestFormatTimeAndPointer(t *testing.T) {
	if got := FormatTime(time.Time{}); got != "-" {
		t.Errorf("zero time should render as -, got %q", got)
	}
	if got := FormatTimePtr(nil); got != "-" {
		t.Errorf("nil time should render as -, got %q", got)
	}

	stamp := time.Date(2026, 7, 30, 12, 0, 0, 0, time.UTC)
	if got := FormatTimePtr(&stamp); got != "2026-07-30T12:00:00Z" {
		t.Errorf("FormatTimePtr = %q", got)
	}
}

func TestFormatDurationUsesASingleUnit(t *testing.T) {
	cases := []struct {
		in   time.Duration
		want string
	}{
		{45 * time.Second, "45s"},
		{90 * time.Second, "1m"},
		{3 * time.Hour, "3h"},
		{50 * time.Hour, "2d"},
	}
	for _, tc := range cases {
		if got := FormatDuration(tc.in); got != tc.want {
			t.Errorf("FormatDuration(%s) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestFormatTagsIsStable(t *testing.T) {
	if got := FormatTags(nil); got != "-" {
		t.Errorf("empty tags should render as -, got %q", got)
	}

	tags := map[string]string{"zone": "eu", "env": "prod", "team": "core"}
	want := "env=prod,team=core,zone=eu"
	// Map iteration order is randomized, so check repeatedly.
	for i := 0; i < 20; i++ {
		if got := FormatTags(tags); got != want {
			t.Fatalf("FormatTags = %q, want %q", got, want)
		}
	}
}

func TestParseKeyValues(t *testing.T) {
	parsed, err := ParseKeyValues([]string{"env=prod", " team = core ", "empty="})
	if err != nil {
		t.Fatalf("ParseKeyValues: %v", err)
	}
	if parsed["env"] != "prod" {
		t.Errorf("env = %q", parsed["env"])
	}
	if parsed["team"] != " core " {
		t.Errorf("values should keep their spacing, got %q", parsed["team"])
	}
	if _, ok := parsed["empty"]; !ok {
		t.Error("an explicitly empty value should still be set")
	}

	if parsed, err := ParseKeyValues(nil); parsed != nil || err != nil {
		t.Errorf("no pairs should yield (nil, nil), got (%v, %v)", parsed, err)
	}
	if _, err := ParseKeyValues([]string{"novalue"}); err == nil {
		t.Error("a pair without = should be rejected")
	}
	if _, err := ParseKeyValues([]string{"=orphan"}); err == nil {
		t.Error("a pair without a key should be rejected")
	}
}

func TestYesNoAndOrDash(t *testing.T) {
	if YesNo(true) != "yes" || YesNo(false) != "no" {
		t.Error("YesNo should render yes/no")
	}
	if OrDash("") != "-" || OrDash("value") != "value" {
		t.Error("OrDash should replace only empty strings")
	}
}

func TestPrintWritesALine(t *testing.T) {
	out := captureOut(t, func() { Print("machine %s on port %d", "web-01", 22) })
	if out != "machine web-01 on port 22\n" {
		t.Errorf("Print wrote %q", out)
	}
}
