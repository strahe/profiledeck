package main

import (
	"sync"
	"time"

	"github.com/strahe/profiledeck/desktop/backend"
)

type desktopChangeDebouncer struct {
	mu         sync.Mutex
	delay      time.Duration
	callback   func(backend.DesktopChangeEvent)
	timer      *time.Timer
	generation uint64
	last       backend.DesktopChangeEvent
	stopped    bool
}

func newDesktopChangeDebouncer(delay time.Duration, callback func(backend.DesktopChangeEvent)) *desktopChangeDebouncer {
	return &desktopChangeDebouncer{delay: delay, callback: callback}
}

func (d *desktopChangeDebouncer) Notify(event backend.DesktopChangeEvent) {
	if d == nil || d.callback == nil {
		return
	}

	d.mu.Lock()
	if d.stopped {
		d.mu.Unlock()
		return
	}
	d.generation++
	generation := d.generation
	d.last = event
	if d.timer != nil {
		d.timer.Stop()
	}
	// The generation guard makes callbacks from stopped-but-already-queued timers
	// harmless, so a stale refresh cannot overwrite the latest dashboard event.
	d.timer = time.AfterFunc(d.delay, func() {
		d.mu.Lock()
		if d.stopped || generation != d.generation {
			d.mu.Unlock()
			return
		}
		event := d.last
		d.timer = nil
		d.mu.Unlock()

		d.callback(event)
	})
	d.mu.Unlock()
}

func (d *desktopChangeDebouncer) Stop() {
	if d == nil {
		return
	}

	d.mu.Lock()
	d.stopped = true
	d.generation++
	if d.timer != nil {
		d.timer.Stop()
		d.timer = nil
	}
	d.mu.Unlock()
}
