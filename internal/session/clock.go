// Package session implements Benny512's stateful protocol engines: Art-Net
// node discovery/liveness (ArtNetSession), the RDM-over-Art-Net controller
// state machine (RDMController), and the DMX output engine
// (DMXOutputEngine).
//
// Layering rule (architecture rev 5 §2): nothing in this package touches a
// socket. Engines take an injected Transport (raw UDP payload bytes plus a
// source/destination address) and an injected Clock, so the entire package
// is exercised in tests with a fake clock and a scripted transport — no
// sleeps, no sockets, no wall-clock flakiness.
package session

import (
	"sync"
	"time"
)

// Timer is a one-shot scheduled callback, as returned by Clock.AfterFunc.
// Stop cancels it; it is safe to call Stop after the timer has fired.
type Timer interface {
	Stop()
}

// Clock abstracts time so engines can be driven deterministically in tests.
//
// AfterFunc must never invoke f synchronously from inside AfterFunc itself:
// engines call AfterFunc while holding their own mutex, so a synchronous
// callback would deadlock. RealClock satisfies this trivially; FakeClock
// only fires callbacks from Advance/Set.
type Clock interface {
	Now() time.Time
	AfterFunc(d time.Duration, f func()) Timer
}

// RealClock is the production Clock, backed by the time package.
type RealClock struct{}

// Now returns the current wall-clock time.
func (RealClock) Now() time.Time { return time.Now() }

// AfterFunc schedules f to run on its own goroutine after d.
func (RealClock) AfterFunc(d time.Duration, f func()) Timer {
	return realTimer{time.AfterFunc(d, f)}
}

type realTimer struct{ t *time.Timer }

func (t realTimer) Stop() { t.t.Stop() }

// FakeClock is a manually-advanced Clock for tests. Time only moves when
// Advance or Set is called, and every timer whose deadline is crossed fires
// synchronously, in deadline order (ties broken by scheduling order), on the
// goroutine calling Advance. That makes engine behaviour fully deterministic:
// when Advance returns, every consequence of the elapsed time has already
// happened.
type FakeClock struct {
	mu     sync.Mutex
	now    time.Time
	nextID uint64
	timers map[uint64]*fakeTimer
}

type fakeTimer struct {
	id       uint64
	deadline time.Time
	f        func()
	clock    *FakeClock
}

func (t *fakeTimer) Stop() {
	t.clock.mu.Lock()
	delete(t.clock.timers, t.id)
	t.clock.mu.Unlock()
}

// NewFakeClock returns a FakeClock starting at start. A zero start is
// replaced with a fixed, non-zero epoch so that "unset time" bugs surface as
// obviously-wrong values rather than as a plausible zero.
func NewFakeClock(start time.Time) *FakeClock {
	if start.IsZero() {
		start = time.Date(2026, 8, 9, 22, 36, 0, 0, time.UTC)
	}
	return &FakeClock{now: start, timers: make(map[uint64]*fakeTimer)}
}

// Now returns the fake clock's current time.
func (c *FakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

// AfterFunc schedules f at now+d. A non-positive d schedules at the current
// instant, which still only fires on the next Advance/Set — never inline.
func (c *FakeClock) AfterFunc(d time.Duration, f func()) Timer {
	c.mu.Lock()
	defer c.mu.Unlock()
	if d < 0 {
		d = 0
	}
	c.nextID++
	t := &fakeTimer{id: c.nextID, deadline: c.now.Add(d), f: f, clock: c}
	c.timers[t.id] = t
	return t
}

// Advance moves time forward by d, firing every timer whose deadline is
// reached, in deadline order. Callbacks that schedule further timers within
// the advanced window are fired too, so a chain of short retries resolves in
// a single Advance call.
func (c *FakeClock) Advance(d time.Duration) {
	if d < 0 {
		return
	}
	c.mu.Lock()
	target := c.now.Add(d)
	c.mu.Unlock()
	c.Set(target)
}

// Set moves time to target (never backwards), firing timers along the way.
func (c *FakeClock) Set(target time.Time) {
	for {
		c.mu.Lock()
		if target.Before(c.now) {
			c.mu.Unlock()
			return
		}
		var next *fakeTimer
		for _, t := range c.timers {
			if t.deadline.After(target) {
				continue
			}
			if next == nil || t.deadline.Before(next.deadline) ||
				(t.deadline.Equal(next.deadline) && t.id < next.id) {
				next = t
			}
		}
		if next == nil {
			c.now = target
			c.mu.Unlock()
			return
		}
		delete(c.timers, next.id)
		if next.deadline.After(c.now) {
			c.now = next.deadline
		}
		c.mu.Unlock()
		next.f()
	}
}

// PendingTimers reports how many timers are currently scheduled. Tests use
// it to assert that engines clean up after themselves (no timer leaks).
func (c *FakeClock) PendingTimers() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.timers)
}
