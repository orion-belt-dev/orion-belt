package recording

import "testing"

func TestSessionHub_Watchers(t *testing.T) {
	h := NewSessionHub()
	if n := h.Watchers("s"); n != 0 {
		t.Fatalf("watchers of an unknown session = %d", n)
	}
	a, b := h.Subscribe("s"), h.Subscribe("s")
	if n := h.Watchers("s"); n != 2 {
		t.Errorf("watchers = %d, want 2", n)
	}
	h.Unsubscribe("s", a)
	if n := h.Watchers("s"); n != 1 {
		t.Errorf("after one leaves = %d, want 1", n)
	}
	h.CloseSession("s")
	if _, open := <-b; open || h.Watchers("s") != 0 {
		t.Error("CloseSession left a watcher subscribed")
	}
}
