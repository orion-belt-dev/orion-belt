// Package livesessions tracks sessions in flight so shutdown can end them
// and wait until each has written its recording and closed its record.
package livesessions

import (
	"context"
	"sync"
)

// Tracker records live sessions. Recordings are only written when a session
// ends; without the wait, stopping the server lost the recording of every
// session still open. The zero value is ready to use.
type Tracker struct {
	mu      sync.Mutex
	closing bool
	stops   map[string]func()
	wg      sync.WaitGroup
}

// Begin registers a session and the function that ends it early. It returns
// false once shutdown has started; the caller must then refuse the session.
func (l *Tracker) Begin(id string, stop func()) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.closing {
		return false
	}
	if l.stops == nil {
		l.stops = map[string]func(){}
	}
	l.stops[id] = stop
	// Under mu, so every Add happens before Drain's Wait.
	l.wg.Add(1)
	return true
}

// End marks a session finished, after its recording is written.
func (l *Tracker) End(id string) {
	l.mu.Lock()
	delete(l.stops, id)
	l.mu.Unlock()
	l.wg.Done()
}

// Drain refuses new sessions, ends the live ones and waits for them to
// finish, or for ctx to expire.
func (l *Tracker) Drain(ctx context.Context) error {
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
