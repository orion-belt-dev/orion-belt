package recording

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/orion-belt-dev/orion-belt/pkg/common"
)

func TestCastRecordingOutputOnly(t *testing.T) {
	dir := t.TempDir()
	rec, err := NewRecorder(dir, common.NewLogger(common.ERROR))
	if err != nil {
		t.Fatal(err)
	}
	rec.SetCompression("none")

	sr, err := rec.StartRecordingSized("sess-1", 80, 24, "lab")
	if err != nil {
		t.Fatal(err)
	}

	if err := sr.Write([]byte("hello")); err != nil {
		t.Fatal(err)
	}
	time.Sleep(20 * time.Millisecond)
	if err := sr.Write([]byte(" world")); err != nil {
		t.Fatal(err)
	}
	if err := sr.RecordResize(100, 30); err != nil {
		t.Fatal(err)
	}

	if err := rec.StopRecording("sess-1"); err != nil {
		t.Fatal(err)
	}

	path := filepath.Join(dir, "sess-1.cast")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(string(raw)), "\n")
	if len(lines) < 3 {
		t.Fatalf("expected header + events, got %d lines: %q", len(lines), raw)
	}

	var hdr castHeader
	if err := json.Unmarshal([]byte(lines[0]), &hdr); err != nil {
		t.Fatal(err)
	}
	if hdr.Version != 2 || hdr.Width != 80 || hdr.Height != 24 || hdr.Title != "lab" {
		t.Fatalf("bad header: %+v", hdr)
	}

	var ev []interface{}
	if err := json.Unmarshal([]byte(lines[1]), &ev); err != nil {
		t.Fatal(err)
	}
	if len(ev) != 3 || ev[1] != "o" || ev[2] != "hello" {
		t.Fatalf("bad first event: %#v", ev)
	}

	// Ensure control sequences are preserved (not stripped).
	sr2, err := rec.StartRecording("sess-2")
	if err != nil {
		t.Fatal(err)
	}
	csi := "\x1b[32mgreen\x1b[0m"
	if err := sr2.Write([]byte(csi)); err != nil {
		t.Fatal(err)
	}
	if err := rec.StopRecording("sess-2"); err != nil {
		t.Fatal(err)
	}
	raw2, _ := os.ReadFile(filepath.Join(dir, "sess-2.cast"))
	if !strings.Contains(string(raw2), `\u001b[32mgreen\u001b[0m`) && !strings.Contains(string(raw2), csi) {
		t.Fatalf("expected CSI preserved in cast, got %q", raw2)
	}
}
