package recording

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/orion-belt-dev/orion-belt/pkg/common"
)

func newTestRecorder(t *testing.T) (*Recorder, string, *bytes.Buffer) {
	t.Helper()
	var logs bytes.Buffer
	dir := t.TempDir()
	rec, err := NewRecorder(dir, common.NewLoggerTo(common.DEBUG, &logs))
	if err != nil {
		t.Fatalf("NewRecorder: %v", err)
	}
	return rec, dir, &logs
}

// --- compression -------------------------------------------------------------

func TestMaybeCompress_UnsupportedMode_ReturnsError(t *testing.T) {
	if _, err := maybeCompress([]byte("x"), "zstd"); err == nil || !strings.Contains(err.Error(), `"zstd"`) {
		t.Fatalf("maybeCompress(zstd) error = %v, want unsupported-mode error", err)
	}
}

func TestMaybeDecompress_CorruptPayloads_ReturnErrors(t *testing.T) {
	good, err := maybeCompress(bytes.Repeat([]byte("frame;"), 100), "gzip")
	if err != nil {
		t.Fatalf("maybeCompress: %v", err)
	}
	cases := map[string]struct {
		data []byte
		want string
	}{
		"bad header":     {append([]byte(gzipMagic), "not gzip"...), "gzip header"},
		"truncated body": {good[:len(good)-10], "gzip read"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := MaybeDecompress(tc.data); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Errorf("MaybeDecompress error = %v, want %q", err, tc.want)
			}
		})
	}
}

// --- crypto ------------------------------------------------------------------

func TestCryptoDecrypt_EncryptedDataWithoutKey_NamesTheConfigKey(t *testing.T) {
	enc, err := mustCrypto(t, "a key").Encrypt([]byte("secret"))
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}
	_, err = mustCrypto(t, "").Decrypt(enc)
	if err == nil || !strings.Contains(err.Error(), "encryption_key is not configured") {
		t.Fatalf("Decrypt error = %v, want the missing encryption_key message", err)
	}
}

func TestCrypto_EncryptAndWriteThenDecryptFile_RoundTrips(t *testing.T) {
	c := mustCrypto(t, "a key")
	path := filepath.Join(t.TempDir(), "rec.cast")
	if err := c.EncryptAndWrite(path, []byte("payload")); err != nil {
		t.Fatalf("EncryptAndWrite: %v", err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Errorf("recording file mode = %o, want 600", perm)
	}
	raw, _ := os.ReadFile(path)
	if bytes.Contains(raw, []byte("payload")) {
		t.Error("file on disk contains the plaintext")
	}
	got, err := c.DecryptFile(path)
	if err != nil || string(got) != "payload" {
		t.Errorf("DecryptFile = %q, %v; want payload", got, err)
	}
}

func TestCryptoDecryptFile_MissingFile_ReturnsError(t *testing.T) {
	_, err := mustCrypto(t, "a key").DecryptFile(filepath.Join(t.TempDir(), "absent"))
	if !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("DecryptFile error = %v, want ErrNotExist", err)
	}
}

func TestCryptoEncryptAndWrite_UnwritablePath_ReturnsError(t *testing.T) {
	path := filepath.Join(t.TempDir(), "missing-dir", "rec.cast")
	if err := mustCrypto(t, "a key").EncryptAndWrite(path, []byte("x")); err == nil {
		t.Fatal("EncryptAndWrite into a missing directory succeeded")
	}
}

func mustCrypto(t *testing.T, key string) *Crypto {
	t.Helper()
	c, err := NewCrypto(key, nil)
	if err != nil {
		t.Fatalf("NewCrypto: %v", err)
	}
	return c
}

// --- retention ---------------------------------------------------------------

func TestEnforceRetention_EdgeCases(t *testing.T) {
	t.Run("disabled retention deletes nothing", func(t *testing.T) {
		n, err := EnforceRetention(t.TempDir(), 0, nil)
		if n != 0 || err != nil {
			t.Errorf("EnforceRetention(0 days) = %d, %v; want 0, nil", n, err)
		}
	})
	t.Run("missing storage path is not an error", func(t *testing.T) {
		n, err := EnforceRetention(filepath.Join(t.TempDir(), "absent"), 7, nil)
		if n != 0 || err != nil {
			t.Errorf("EnforceRetention(missing) = %d, %v; want 0, nil", n, err)
		}
	})
	t.Run("storage path that is a file is an error", func(t *testing.T) {
		f := filepath.Join(t.TempDir(), "file")
		if err := os.WriteFile(f, nil, 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := EnforceRetention(f, 7, nil); err == nil {
			t.Error("EnforceRetention on a regular file succeeded")
		}
	})
}

func TestEnforceRetention_DeletesOnlyOldRecordingFiles(t *testing.T) {
	dir := t.TempDir()
	old := time.Now().AddDate(0, 0, -30)
	write := func(name string, mod time.Time) {
		t.Helper()
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.Chtimes(p, mod, mod); err != nil {
			t.Fatal(err)
		}
	}
	write("old.cast", old)
	write("old.rec", old)
	write("old.txt", old)
	write("old.json", old) // not a recording extension
	write("new.cast", time.Now())
	if err := os.Mkdir(filepath.Join(dir, "old-dir.cast"), 0o700); err != nil {
		t.Fatal(err)
	}

	var logs bytes.Buffer
	n, err := EnforceRetention(dir, 7, common.NewLoggerTo(common.DEBUG, &logs))
	if err != nil || n != 3 {
		t.Fatalf("EnforceRetention = %d, %v; want 3, nil", n, err)
	}
	for _, kept := range []string{"old.json", "new.cast", "old-dir.cast"} {
		if _, err := os.Stat(filepath.Join(dir, kept)); err != nil {
			t.Errorf("%s was removed: %v", kept, err)
		}
	}
	if !strings.Contains(logs.String(), "deleted 3 recording(s) older than 7 days") {
		t.Errorf("log = %q, want the deletion summary", logs.String())
	}
}

func TestEnforceRetention_UndeletableFile_IsLoggedAndSkipped(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root ignores directory write permission, so the delete cannot be made to fail")
	}
	dir := t.TempDir()
	p := filepath.Join(dir, "old.cast")
	if err := os.WriteFile(p, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	old := time.Now().AddDate(0, 0, -30)
	if err := os.Chtimes(p, old, old); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(dir, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o700) })

	var logs bytes.Buffer
	n, err := EnforceRetention(dir, 7, common.NewLoggerTo(common.DEBUG, &logs))
	if err != nil || n != 0 {
		t.Fatalf("EnforceRetention = %d, %v; want 0, nil", n, err)
	}
	if !strings.Contains(logs.String(), "retention: failed to delete") {
		t.Errorf("log = %q, want the delete failure", logs.String())
	}
}

// --- hub -----------------------------------------------------------------------

func TestSessionHub_UnsubscribeKeepsOtherWatchers(t *testing.T) {
	h := NewSessionHub()
	a := h.Subscribe("s")
	b := h.Subscribe("s")

	h.Unsubscribe("s", a)
	if _, open := <-a; open {
		t.Error("unsubscribed channel is still open")
	}
	h.Broadcast("s", []byte("hi"))
	if got := <-b; string(got) != "hi" {
		t.Errorf("remaining watcher got %q, want hi", got)
	}

	h.Unsubscribe("s", b)
	h.Unsubscribe("s", b)        // already gone: no double close
	h.Unsubscribe("absent", nil) // unknown session: no-op
	if len(h.subs) != 0 {
		t.Errorf("hub still tracks %d sessions after all watchers left", len(h.subs))
	}
}

func TestSessionHub_BroadcastDropsForSlowWatcherAndIgnoresEmpty(t *testing.T) {
	h := NewSessionHub()
	ch := h.Subscribe("s")

	h.Broadcast("s", nil)
	var nilHub *SessionHub
	nilHub.Broadcast("s", []byte("x"))

	for i := 0; i < cap(ch)+10; i++ {
		h.Broadcast("s", []byte{byte(i)})
	}
	if len(ch) != cap(ch) {
		t.Fatalf("buffered %d messages, want the channel full at %d", len(ch), cap(ch))
	}
	if first := <-ch; first[0] != 0 {
		t.Errorf("first message = %v, want the earliest broadcast kept", first)
	}
}

func TestSessionHub_CloseSessionClosesAllWatchers(t *testing.T) {
	h := NewSessionHub()
	a, b := h.Subscribe("s"), h.Subscribe("s")
	h.CloseSession("s")
	for _, ch := range []<-chan []byte{a, b} {
		if _, open := <-ch; open {
			t.Error("watcher channel still open after CloseSession")
		}
	}
}

// --- recorder ------------------------------------------------------------------

func TestNewRecorder_UncreatableDirectory_ReturnsError(t *testing.T) {
	f := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(f, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := NewRecorder(filepath.Join(f, "sub"), nil); err == nil {
		t.Fatal("NewRecorder under a regular file succeeded")
	}
}

func TestRecorder_Accessors(t *testing.T) {
	rec, dir, _ := newTestRecorder(t)
	if rec.GetRecordingStoragePath() != dir {
		t.Errorf("GetRecordingStoragePath = %q, want %q", rec.GetRecordingStoragePath(), dir)
	}

	rec.SetCompression("none")
	rec.SetCompression("")
	if rec.compression != "gzip" {
		t.Errorf("SetCompression(\"\") left %q, want gzip default", rec.compression)
	}

	rec.hub = nil
	if rec.Hub() == nil || rec.Hub() != rec.Hub() {
		t.Error("Hub() must lazily create one hub and keep returning it")
	}
}

func TestRecorder_StartTwiceAndStopUnknown_ReturnErrors(t *testing.T) {
	rec, _, _ := newTestRecorder(t)
	if _, err := rec.StartRecording("s"); err != nil {
		t.Fatalf("StartRecording: %v", err)
	}
	if _, err := rec.StartRecording("s"); err == nil {
		t.Error("second StartRecording for the same session succeeded")
	}
	if err := rec.StopRecording("other"); err == nil {
		t.Error("StopRecording of an unknown session succeeded")
	}
	if _, err := rec.GetRecorder("other"); err == nil {
		t.Error("GetRecorder of an unknown session succeeded")
	}
	sr, err := rec.GetRecorder("s")
	if err != nil || sr.sessionID != "s" {
		t.Errorf("GetRecorder(s) = %v, %v; want the active recorder", sr, err)
	}
}

func TestRecorder_NonPositiveSizeUsesDefaults(t *testing.T) {
	rec, _, _ := newTestRecorder(t)
	sr, err := rec.StartRecordingSized("s", 0, -1, "")
	if err != nil {
		t.Fatalf("StartRecordingSized: %v", err)
	}
	if sr.width != 120 || sr.height != 40 {
		t.Errorf("size = %dx%d, want 120x40 default", sr.width, sr.height)
	}
}

func TestRecorder_EncryptedGzipRecording_RoundTrips(t *testing.T) {
	rec, dir, _ := newTestRecorder(t)
	c := mustCrypto(t, "recording key")
	rec.SetCrypto(c)

	sr, err := rec.StartRecording("s")
	if err != nil {
		t.Fatalf("StartRecording: %v", err)
	}
	_ = sr.Write([]byte("top secret output"))
	if err := rec.StopRecording("s"); err != nil {
		t.Fatalf("StopRecording: %v", err)
	}

	sealed, err := c.DecryptFile(filepath.Join(dir, "s.cast"))
	if err != nil {
		t.Fatalf("DecryptFile: %v", err)
	}
	plain, err := MaybeDecompress(sealed)
	if err != nil {
		t.Fatalf("MaybeDecompress: %v", err)
	}
	if !bytes.Contains(plain, []byte("top secret output")) {
		t.Errorf("recovered cast %q lacks the recorded output", plain)
	}
}

func TestRecorder_UnsupportedCompression_FallsBackToPlain(t *testing.T) {
	rec, dir, logs := newTestRecorder(t)
	rec.SetCompression("lz4")
	sr, _ := rec.StartRecording("s")
	_ = sr.Write([]byte("output"))

	if err := rec.StopRecording("s"); err != nil {
		t.Fatalf("StopRecording: %v", err)
	}
	raw, _ := os.ReadFile(filepath.Join(dir, "s.cast"))
	if !bytes.Contains(raw, []byte("output")) {
		t.Errorf("file %q, want an uncompressed cast", raw)
	}
	if !strings.Contains(logs.String(), "Failed to compress recording") {
		t.Errorf("log = %q, want the compression failure", logs.String())
	}
}

func TestRecorder_WriteFailure_IsReturnedAndSessionReleased(t *testing.T) {
	rec, dir, logs := newTestRecorder(t)
	if _, err := rec.StartRecording("s"); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(dir); err != nil {
		t.Fatal(err)
	}

	if err := rec.StopRecording("s"); err == nil {
		t.Fatal("StopRecording into a deleted directory succeeded")
	}
	if !strings.Contains(logs.String(), "Failed to write recording") {
		t.Errorf("log = %q, want the write failure", logs.String())
	}
	if _, err := rec.GetRecorder("s"); err == nil {
		t.Error("session still registered after a failed stop")
	}
}

func TestSessionRecorder_EmptyWriteAndZeroResizeRecordNothing(t *testing.T) {
	rec, _, _ := newTestRecorder(t)
	sr, _ := rec.StartRecording("s")
	before := sr.buf.Len()

	if err := sr.Write(nil); err != nil {
		t.Fatalf("Write(nil): %v", err)
	}
	if err := sr.RecordResize(0, 40); err != nil {
		t.Fatalf("RecordResize(0, 40): %v", err)
	}
	if sr.buf.Len() != before {
		t.Errorf("buffer grew from %d to %d on no-op events", before, sr.buf.Len())
	}
}

func TestRecordingWriter_RecordsAndForwards(t *testing.T) {
	rec, _, _ := newTestRecorder(t)
	sr, _ := rec.StartRecording("s")

	var out bytes.Buffer
	n, err := NewRecordingWriter(&out, sr).Write([]byte("hello"))
	if err != nil || n != 5 || out.String() != "hello" {
		t.Fatalf("Write = %d, %v (out %q); want 5 bytes forwarded", n, err, out.String())
	}
	if !strings.Contains(sr.buf.String(), `"o","hello"`) {
		t.Errorf("cast buffer %q lacks the output event", sr.buf.String())
	}

	out.Reset()
	if _, err := NewRecordingWriter(&out, nil).Write([]byte("x")); err != nil || out.String() != "x" {
		t.Errorf("writer without recorder: out=%q err=%v, want pass-through", out.String(), err)
	}
}

func TestRecordingReader_PassesThroughWithoutRecording(t *testing.T) {
	rec, _, _ := newTestRecorder(t)
	sr, _ := rec.StartRecording("s")
	before := sr.buf.Len()

	buf := make([]byte, 16)
	n, err := NewRecordingReader(strings.NewReader("typed"), sr).Read(buf)
	if err != nil || string(buf[:n]) != "typed" {
		t.Fatalf("Read = %q, %v; want typed", buf[:n], err)
	}
	if sr.buf.Len() != before {
		t.Error("RecordingReader recorded input; casts must be output-only")
	}
}
