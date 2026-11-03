package server

import (
	"context"
	"sync"
)

// liveSessions tracks the proxied sessions in flight so shutdown can end them
// and wait until each has written its recording and closed its database
// record. Recordings are only written when a session ends; without the wait,
// stopping the server lost the recording of every session still open.
type liveSessions struct {
	mu      sync.Mutex
	closing bool
	stops   map[string]func()
	wg      sync.WaitGroup
}

// begin registers a session and the function that ends it early. It returns
// false once shutdown has started; the caller must then refuse the session.
func (l *liveSessions) begin(id string, stop func()) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.closing {
		return false
	}
	if l.stops == nil {
		l.stops = map[string]func(){}
	}
	l.stops[id] = stop
	// Under mu, so every Add happens before drain's Wait.
	l.wg.Add(1)
	return true
}

// end marks a session finished, after its recording is written.
func (l *liveSessions) end(id string) {
	l.mu.Lock()
	delete(l.stops, id)
	l.mu.Unlock()
	l.wg.Done()
}

// drain refuses new sessions, ends the live ones and waits for them to
// finish, or for ctx to expire.
func (l *liveSessions) drain(ctx context.Context) error {
	l.mu.Lock()
	l.closing = true
	stops := make([]func(), 0, len(l.stops))
	for _, stop := range l.stops {
		stops = append(stops, stop)
	}
	l.mu.Unlock()
	for _, stop := range stops {
		stop()
	}

	done := make(chan struct{})
	go func() {
		l.wg.Wait()
		close(done)
	}()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
