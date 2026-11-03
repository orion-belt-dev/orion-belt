package api

import (
	"fmt"
	"testing"
	"time"
)

// Keys are client IPs on public routes; they must not accumulate.
func TestRateLimiter_ForgetsIdleKeys(t *testing.T) {
	clock := time.Date(2026, 11, 3, 12, 0, 0, 0, time.UTC)
	r := newRateLimiter(2, time.Minute)
	r.now = func() time.Time { return clock }

	for i := 0; i < 1000; i++ {
		if !r.allow(fmt.Sprintf("2001:db8::%x", i)) {
			t.Fatalf("first request from address %d refused", i)
		}
	}
	if !r.allow("busy") || !r.allow("busy") || r.allow("busy") {
		t.Fatal("limit not enforced within the window")
	}

	clock = clock.Add(time.Minute + time.Second)
	if !r.allow("busy") {
		t.Error("key still limited after its window passed")
	}
	if n := len(r.requests); n != 1 {
		t.Errorf("keys kept after a window of silence = %d, want only the one just seen", n)
	}
}
