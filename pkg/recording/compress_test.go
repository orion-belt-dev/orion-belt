package recording

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math/rand"
	"strings"
	"testing"
)

func TestMaybeCompressRoundTrip(t *testing.T) {
	plain := []byte("{\"version\":2}\n[0,\"o\",\"hi\"]\n")
	gz, err := maybeCompress(plain, "gzip")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.HasPrefix(gz, []byte(gzipMagic)) {
		t.Fatalf("missing magic: %q", gz[:min(8, len(gz))])
	}
	out, err := MaybeDecompress(gz)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(out, plain) {
		t.Fatalf("round-trip mismatch: %q vs %q", out, plain)
	}
}

func TestMaybeDecompressPlain(t *testing.T) {
	plain := []byte("not compressed")
	out, err := MaybeDecompress(plain)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(out, plain) {
		t.Fatal("plain data should pass through")
	}
}

func TestMaybeCompressNone(t *testing.T) {
	plain := []byte("plain cast")
	out, err := maybeCompress(plain, "none")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(out, plain) {
		t.Fatal("none should skip compression")
	}
}

// sampleCasts builds deterministic asciicast v2 recordings shaped like real
// sessions: interactive shell work, a tailed service log, and a full-screen
// editor redrawing with ANSI escapes. Random fields keep them from being
// trivially compressible.
func sampleCasts() map[string][]byte {
	rng := rand.New(rand.NewSource(42))
	header := func(buf *bytes.Buffer) {
		buf.WriteString(`{"version":2,"width":120,"height":40,"timestamp":1760000000,"env":{"SHELL":"/bin/bash","TERM":"xterm-256color"}}` + "\n")
	}
	event := func(buf *bytes.Buffer, t float64, kind, data string) {
		line, _ := json.Marshal([]interface{}{t, kind, data})
		buf.Write(line)
		buf.WriteByte('\n')
	}
	hex := func(n int) string {
		b := make([]byte, n/2)
		rng.Read(b)
		return fmt.Sprintf("%x", b)
	}

	casts := map[string][]byte{}

	var shell bytes.Buffer
	header(&shell)
	t := 0.0
	cmds := []string{"ls -la /var/log", "df -h", "ps aux | head -40", "systemctl status nginx", "git log --oneline -20"}
	for i := 0; i < 60; i++ {
		t += rng.Float64() * 2
		event(&shell, t, "o", "\x1b[32mops@web-01\x1b[0m:\x1b[34m~\x1b[0m$ ")
		cmd := cmds[rng.Intn(len(cmds))]
		for _, c := range cmd {
			t += 0.05 + rng.Float64()*0.1
			event(&shell, t, "o", string(c))
		}
		event(&shell, t, "o", "\r\n")
		for j := 0; j < 20+rng.Intn(20); j++ {
			t += rng.Float64() * 0.01
			event(&shell, t, "o", fmt.Sprintf("-rw-r----- 1 root adm %8d Oct %2d %02d:%02d %s.log\r\n",
				rng.Intn(1<<24), 1+rng.Intn(28), rng.Intn(24), rng.Intn(60), hex(12)))
		}
	}
	casts["shell"] = shell.Bytes()

	var logs bytes.Buffer
	header(&logs)
	t = 0
	levels := []string{"INFO", "INFO", "INFO", "WARN", "ERROR"}
	for i := 0; i < 3000; i++ {
		t += rng.Float64() * 0.2
		event(&logs, t, "o", fmt.Sprintf("2026-10-23T%02d:%02d:%02d.%03dZ %-5s request_id=%s method=GET path=/api/v1/machines/%s status=%d duration_ms=%d\r\n",
			rng.Intn(24), rng.Intn(60), rng.Intn(60), rng.Intn(1000), levels[rng.Intn(len(levels))],
			hex(16), hex(8), []int{200, 200, 200, 404, 500}[rng.Intn(5)], rng.Intn(900)))
	}
	casts["log-tail"] = logs.Bytes()

	var editor bytes.Buffer
	header(&editor)
	t = 0
	for i := 0; i < 400; i++ {
		t += rng.Float64() * 0.3
		var frame strings.Builder
		frame.WriteString("\x1b[H\x1b[2J")
		for row := 1; row <= 38; row++ {
			fmt.Fprintf(&frame, "\x1b[%d;1H\x1b[33m%4d\x1b[0m  server:\r\n    listen: 0.0.0.0:%d\r\n    token: %s", row, row, 1024+rng.Intn(60000), hex(10))
		}
		fmt.Fprintf(&frame, "\x1b[40;1H\x1b[7m config.yaml  line %d \x1b[0m", 1+rng.Intn(200))
		event(&editor, t, "o", frame.String())
	}
	casts["editor"] = editor.Bytes()

	return casts
}

// TestCompressionReducesFootprint checks that gzip at least halves the size
// of representative recordings and that each one plays back unchanged.
func TestCompressionReducesFootprint(t *testing.T) {
	var plainTotal, gzTotal int
	for name, plain := range sampleCasts() {
		gz, err := maybeCompress(plain, "gzip")
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		out, err := MaybeDecompress(gz)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if !bytes.Equal(out, plain) {
			t.Fatalf("%s: playback bytes differ after compression", name)
		}
		ratio := float64(len(gz)) / float64(len(plain))
		t.Logf("%-8s %8d -> %7d bytes (%.1f%%)", name, len(plain), len(gz), ratio*100)
		if ratio > 0.5 {
			t.Errorf("%s: compressed to %.1f%% of original, want <= 50%%", name, ratio*100)
		}
		plainTotal += len(plain)
		gzTotal += len(gz)
	}
	t.Logf("total    %8d -> %7d bytes (%.1f%%)", plainTotal, gzTotal, float64(gzTotal)/float64(plainTotal)*100)
}
