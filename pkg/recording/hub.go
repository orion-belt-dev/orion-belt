package recording

import "sync"

// SessionHub fans PTY output out to live watchers of an active session.
type SessionHub struct {
	mu   sync.RWMutex
	subs map[string]map[chan []byte]struct{}
}

// NewSessionHub creates an empty hub.
func NewSessionHub() *SessionHub {
	return &SessionHub{subs: make(map[string]map[chan []byte]struct{})}
}

// Subscribe receives a copy of subsequent PTY output for sessionID.
// Caller must Unsubscribe (or rely on CloseSession).
func (h *SessionHub) Subscribe(sessionID string) <-chan []byte {
	ch := make(chan []byte, 64)
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.subs[sessionID] == nil {
		h.subs[sessionID] = make(map[chan []byte]struct{})
	}
	h.subs[sessionID][ch] = struct{}{}
	return ch
}

// Unsubscribe removes a watcher channel.
func (h *SessionHub) Unsubscribe(sessionID string, ch <-chan []byte) {
	h.mu.Lock()
	defer h.mu.Unlock()
	subs := h.subs[sessionID]
	if subs == nil {
		return
	}
	for c := range subs {
		if c == ch {
			delete(subs, c)
			close(c)
			break
		}
	}
	if len(subs) == 0 {
		delete(h.subs, sessionID)
	}
}

// Broadcast sends a copy of data to all watchers (non-blocking; drops if slow).
func (h *SessionHub) Broadcast(sessionID string, data []byte) {
	if h == nil || len(data) == 0 {
		return
	}
	h.mu.RLock()
	defer h.mu.RUnlock()
	for ch := range h.subs[sessionID] {
		cp := append([]byte(nil), data...)
		select {
		case ch <- cp:
		default:
			// drop if viewer is slow
		}
	}
}

// Watchers returns how many viewers are subscribed to sessionID.
func (h *SessionHub) Watchers(sessionID string) int {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return len(h.subs[sessionID])
}

// CloseSession unsubscribes all watchers for a finished session.
func (h *SessionHub) CloseSession(sessionID string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for ch := range h.subs[sessionID] {
		close(ch)
	}
	delete(h.subs, sessionID)
}
