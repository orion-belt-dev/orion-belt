package recording

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/orion-belt-dev/orion-belt/pkg/common"
)

// Cast v2 header written as the first line of each recording file.
type castHeader struct {
	Version   int               `json:"version"`
	Width     int               `json:"width"`
	Height    int               `json:"height"`
	Timestamp int64             `json:"timestamp"`
	Title     string            `json:"title,omitempty"`
	Env       map[string]string `json:"env,omitempty"`
}

type Recorder struct {
	storagePath string
	logger      *common.Logger
	crypto      *Crypto
	compression string // gzip | none
	hub         *SessionHub
	mu          sync.RWMutex
	sessions    map[string]*SessionRecorder
}

// SessionRecorder records PTY output as a timed cast (version 2).
type SessionRecorder struct {
	sessionID   string
	buf         *bytes.Buffer
	filePath    string
	crypto      *Crypto
	compression string
	hub         *SessionHub
	startTime   time.Time
	width       int
	height      int
	mu          sync.Mutex
}

// NewRecorder creates a new session recorder.
func NewRecorder(storagePath string, logger *common.Logger) (*Recorder, error) {
	if err := os.MkdirAll(storagePath, 0700); err != nil {
		return nil, fmt.Errorf("failed to create storage directory: %w", err)
	}

	return &Recorder{
		storagePath: storagePath,
		logger:      logger,
		compression: "gzip",
		hub:         NewSessionHub(),
		sessions:    make(map[string]*SessionRecorder),
	}, nil
}

// SetCrypto enables at-rest encryption for recordings.
func (r *Recorder) SetCrypto(c *Crypto) {
	r.crypto = c
}

// SetCompression sets flush-time compression (gzip or none).
func (r *Recorder) SetCompression(mode string) {
	if mode == "" {
		mode = "gzip"
	}
	r.compression = mode
}

// Hub returns the live session watch fan-out hub.
func (r *Recorder) Hub() *SessionHub {
	if r.hub == nil {
		r.hub = NewSessionHub()
	}
	return r.hub
}

// StartRecording starts a timed cast recording (default 120×40).
func (r *Recorder) StartRecording(sessionID string) (*SessionRecorder, error) {
	return r.StartRecordingSized(sessionID, 120, 40, "")
}

// StartRecordingSized starts a timed cast with an initial terminal size and optional title.
func (r *Recorder) StartRecordingSized(sessionID string, width, height int, title string) (*SessionRecorder, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if _, exists := r.sessions[sessionID]; exists {
		return nil, fmt.Errorf("session already being recorded: %s", sessionID)
	}
	if width <= 0 {
		width = 120
	}
	if height <= 0 {
		height = 40
	}

	filename := r.GetRecordingPath(sessionID)
	start := time.Now()
	recorder := &SessionRecorder{
		sessionID:   sessionID,
		buf:         &bytes.Buffer{},
		filePath:    filename,
		crypto:      r.crypto,
		compression: r.compression,
		hub:         r.Hub(),
		startTime:   start,
		width:       width,
		height:      height,
	}

	hdr, err := json.Marshal(castHeader{
		Version:   2,
		Width:     width,
		Height:    height,
		Timestamp: start.Unix(),
		Title:     title,
		Env:       map[string]string{"TERM": "xterm-256color"},
	})
	if err != nil {
		return nil, fmt.Errorf("cast header: %w", err)
	}
	recorder.buf.Write(hdr)
	recorder.buf.WriteByte('\n')

	r.sessions[sessionID] = recorder
	r.logger.Info("Started recording session: %s", sessionID)

	return recorder, nil
}

// StopRecording stops recording a session and flushes (optionally compressed/encrypted) to disk.
func (r *Recorder) StopRecording(sessionID string) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	recorder, exists := r.sessions[sessionID]
	if !exists {
		return fmt.Errorf("session not being recorded: %s", sessionID)
	}

	recorder.mu.Lock()
	plain := append([]byte(nil), recorder.buf.Bytes()...)
	compMode := recorder.compression
	path := recorder.filePath
	crypt := recorder.crypto
	recorder.mu.Unlock()

	payload, err := maybeCompress(plain, compMode)
	if err != nil {
		r.logger.Error("Failed to compress recording for session %s: %v", sessionID, err)
		payload = plain
	}

	if crypt != nil && crypt.Enabled() {
		err = crypt.EncryptAndWrite(path, payload)
	} else {
		err = os.WriteFile(path, payload, 0600)
	}
	if err != nil {
		r.logger.Error("Failed to write recording for session %s: %v", sessionID, err)
	}

	if r.hub != nil {
		r.hub.CloseSession(sessionID)
	}
	delete(r.sessions, sessionID)
	r.logger.Info("Stopped recording session: %s", sessionID)

	return err
}

// GetRecorder returns a session recorder.
func (r *Recorder) GetRecorder(sessionID string) (*SessionRecorder, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	recorder, exists := r.sessions[sessionID]
	if !exists {
		return nil, fmt.Errorf("session not being recorded: %s", sessionID)
	}

	return recorder, nil
}

// GetRecordingStoragePath returns the recorder storage directory.
func (r *Recorder) GetRecordingStoragePath() string {
	return r.storagePath
}

// GetRecordingPath returns the on-disk path for a session cast.
func (r *Recorder) GetRecordingPath(sessionID string) string {
	return filepath.Join(r.storagePath, fmt.Sprintf("%s.cast", sessionID))
}

// Write records PTY output as a timed cast event. Raw bytes are kept so the
// player can reconstruct the terminal (do not strip control sequences).
func (s *SessionRecorder) Write(data []byte) error {
	if err := s.writeEvent("o", string(data)); err != nil {
		return err
	}
	if s.hub != nil && len(data) > 0 {
		s.hub.Broadcast(s.sessionID, data)
	}
	return nil
}

// RecordResize records a terminal size change for playback.
func (s *SessionRecorder) RecordResize(cols, rows uint32) error {
	if cols == 0 || rows == 0 {
		return nil
	}
	s.mu.Lock()
	s.width = int(cols)
	s.height = int(rows)
	s.mu.Unlock()
	return s.writeEvent("r", fmt.Sprintf("%dx%d", cols, rows))
}

func (s *SessionRecorder) writeEvent(kind, data string) error {
	if data == "" {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	elapsed := time.Since(s.startTime).Seconds()
	line, err := json.Marshal([]interface{}{elapsed, kind, data})
	if err != nil {
		return fmt.Errorf("cast event: %w", err)
	}
	if _, err := s.buf.Write(line); err != nil {
		return fmt.Errorf("failed to write to recording: %w", err)
	}
	if err := s.buf.WriteByte('\n'); err != nil {
		return fmt.Errorf("failed to write to recording: %w", err)
	}
	return nil
}

// RecordingWriter wraps an io.Writer and records bytes written to the client
// (agent → client PTY output).
type RecordingWriter struct {
	writer   io.Writer
	recorder *SessionRecorder
}

// NewRecordingWriter creates a new recording writer.
func NewRecordingWriter(writer io.Writer, recorder *SessionRecorder) *RecordingWriter {
	return &RecordingWriter{
		writer:   writer,
		recorder: recorder,
	}
}

func (rw *RecordingWriter) Write(p []byte) (n int, err error) {
	if rw.recorder != nil {
		if err := rw.recorder.Write(p); err != nil {
			fmt.Fprintf(os.Stderr, "Recording error: %v\n", err)
		}
	}
	return rw.writer.Write(p)
}

// RecordingReader wraps an io.Reader. It intentionally does not record:
// session casts are output-only so keystrokes are not duplicated with echo.
type RecordingReader struct {
	reader io.Reader
}

// NewRecordingReader creates a passthrough reader (no recording).
func NewRecordingReader(reader io.Reader, _ *SessionRecorder) *RecordingReader {
	return &RecordingReader{reader: reader}
}

func (rr *RecordingReader) Read(p []byte) (n int, err error) {
	return rr.reader.Read(p)
}
