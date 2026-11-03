package livesessions

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"
)

func TestTracker_DrainEndsAndWaits(t *testing.T) {
	var l Tracker
	var stopped atomic.Int32
	release := make(chan struct{})
	if !l.Begin("a", func() { stopped.Add(1); close(release) }) {
		t.Fatal("begin refused before shutdown")
	}
	// The session finishes (writes its recording) only once stopped.
	go func() {
		<-release
		l.End("a")
	}()
	if err := l.Drain(context.Background()); err != nil {
		t.Fatalf("drain: %v", err)
	}
	if stopped.Load() != 1 {
		t.Errorf("stop called %d times, want 1", stopped.Load())
	}
	if l.Begin("b", func() {}) {
		t.Error("begin accepted a session after shutdown started")
	}
}

func TestTracker_DrainGivesUpAtDeadline(t *testing.T) {
	var l Tracker
	l.Begin("stuck", func() {}) // never ends
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if err := l.Drain(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("drain = %v, want the deadline", err)
	}
}
