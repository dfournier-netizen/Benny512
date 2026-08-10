package session

import (
	"testing"
	"time"
)

func TestFakeClockFiresInDeadlineOrder(t *testing.T) {
	c := NewFakeClock(time.Time{})
	var order []string
	c.AfterFunc(30*time.Millisecond, func() { order = append(order, "c") })
	c.AfterFunc(10*time.Millisecond, func() { order = append(order, "a") })
	c.AfterFunc(20*time.Millisecond, func() { order = append(order, "b") })

	c.Advance(25 * time.Millisecond)
	if got, want := len(order), 2; got != want {
		t.Fatalf("fired %d timers, want %d (%v)", got, want, order)
	}
	if order[0] != "a" || order[1] != "b" {
		t.Fatalf("fire order = %v, want [a b]", order)
	}
	c.Advance(10 * time.Millisecond)
	if len(order) != 3 || order[2] != "c" {
		t.Fatalf("fire order = %v, want [a b c]", order)
	}
}

func TestFakeClockNowAtCallbackIsTimerDeadline(t *testing.T) {
	c := NewFakeClock(time.Time{})
	start := c.Now()
	var at time.Time
	c.AfterFunc(7*time.Millisecond, func() { at = c.Now() })
	c.Advance(100 * time.Millisecond)

	if want := start.Add(7 * time.Millisecond); !at.Equal(want) {
		t.Fatalf("callback observed now=%v, want %v", at, want)
	}
	if want := start.Add(100 * time.Millisecond); !c.Now().Equal(want) {
		t.Fatalf("after Advance now=%v, want %v", c.Now(), want)
	}
}

func TestFakeClockStopCancels(t *testing.T) {
	c := NewFakeClock(time.Time{})
	fired := false
	tm := c.AfterFunc(5*time.Millisecond, func() { fired = true })
	tm.Stop()
	c.Advance(time.Second)
	if fired {
		t.Fatal("stopped timer fired")
	}
	if c.PendingTimers() != 0 {
		t.Fatalf("PendingTimers = %d, want 0", c.PendingTimers())
	}
	tm.Stop() // must be safe twice
}

func TestFakeClockAfterFuncNeverFiresInline(t *testing.T) {
	// Engines call AfterFunc while holding their own mutex; firing inline
	// would deadlock them. A zero/negative delay must still wait for
	// Advance.
	c := NewFakeClock(time.Time{})
	fired := false
	c.AfterFunc(0, func() { fired = true })
	c.AfterFunc(-time.Second, func() { fired = true })
	if fired {
		t.Fatal("AfterFunc fired its callback inline")
	}
	c.Advance(0)
	if !fired {
		t.Fatal("timer did not fire on Advance(0)")
	}
}

func TestFakeClockFiresChainedTimersWithinOneAdvance(t *testing.T) {
	c := NewFakeClock(time.Time{})
	n := 0
	var rearm func()
	rearm = func() {
		n++
		if n < 5 {
			c.AfterFunc(10*time.Millisecond, rearm)
		}
	}
	c.AfterFunc(10*time.Millisecond, rearm)
	c.Advance(100 * time.Millisecond)
	if n != 5 {
		t.Fatalf("chained timers fired %d times, want 5", n)
	}
}
