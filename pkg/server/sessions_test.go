package server

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/orion-belt-dev/orion-belt/pkg/database"
)

func TestLiveSessions_DrainEndsAndWaits(t *testing.T) {
	var l liveSessions
	var stopped atomic.Int32
	release := make(chan struct{})
	if !l.begin("a", func() { stopped.Add(1); close(release) }) {
		t.Fatal("begin refused before shutdown")
	}
	// The session finishes (writes its recording) only once stopped.
	go func() {
		<-release
		l.end("a")
	}()
	if err := l.drain(context.Background()); err != nil {
		t.Fatalf("drain: %v", err)
	}
	if stopped.Load() != 1 {
		t.Errorf("stop called %d times, want 1", stopped.Load())
	}
	if l.begin("b", func() {}) {
		t.Error("begin accepted a session after shutdown started")
	}
}

func TestLiveSessions_DrainGivesUpAtDeadline(t *testing.T) {
	var l liveSessions
	l.begin("stuck", func() {}) // never ends
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if err := l.drain(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("drain = %v, want the deadline", err)
	}
}

// Recordings are written when a session ends. Stopping the server used to
// close the store and return while sessions were open, so their recordings
// were lost (and, in tests, written into a directory being removed).
func TestStop_EndsLiveSessionsAndKeepsTheirRecordings(t *testing.T) {
	h := startHarness(t, nil)
	h.connectAgent(t)
	h.grant(t, "deploy")

	c, err := h.dial(t, "alice+deploy%web-01", h.aliceKey)
	if err != nil {
		t.Fatal(err)
	}
	sess, err := c.NewSession()
	if err != nil {
		t.Fatal(err)
	}
	var out strings.Builder
	sess.Stdout = &out
	if err := sess.Start("hang"); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- sess.Wait() }()

	// Wait for the gateway to have the session on record.
	var id string
	for deadline := time.Now().Add(5 * time.Second); id == ""; {
		if time.Now().After(deadline) {
			t.Fatal("session never recorded")
		}
		if list, _ := h.srv.store.ListUserSessions(context.Background(), h.alice.ID, 1, 0); len(list) == 1 {
			id = list[0].ID
		} else {
			time.Sleep(10 * time.Millisecond)
		}
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := h.srv.Stop(ctx); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	if ctx.Err() != nil {
		t.Fatal("Stop waited for its deadline instead of ending the session")
	}
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("client session still open after Stop")
	}
	if !strings.Contains(out.String(), "shutting down") {
		t.Errorf("client output = %q, want the shutdown notice", out.String())
	}
	if _, err := os.Stat(filepath.Join(h.cfg.Recording.StoragePath, id+".cast")); err != nil {
		t.Errorf("recording not written before Stop returned: %v", err)
	}

	// The record was closed before the store was.
	store, err := database.NewStore(h.cfg.Database.Driver, h.cfg.Database.ConnectionString)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	got, err := store.GetSession(context.Background(), id)
	if err != nil || got.EndTime == nil {
		t.Errorf("session after Stop = %+v, %v; want an end time", got, err)
	}
	// Stop is idempotent: the harness calls it again on cleanup.
	if err := h.srv.Stop(context.Background()); err != nil {
		t.Errorf("second Stop: %v", err)
	}
}
