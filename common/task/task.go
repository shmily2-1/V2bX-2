package task

import (
	"fmt"
	"sync"
	"time"
)

type Task struct {
	Interval time.Duration
	Execute  func() error
	access   sync.Mutex
	running  bool
	stop     chan struct{}
	done     chan struct{}
	wake     chan struct{}
}

func (t *Task) Start(first bool) error {
	t.access.Lock()
	if t.running {
		t.access.Unlock()
		return nil
	}
	if t.Interval <= 0 || t.Execute == nil {
		t.access.Unlock()
		return fmt.Errorf("task requires a positive interval and an execute function")
	}
	t.running = true
	t.stop = make(chan struct{})
	t.done = make(chan struct{})
	t.wake = make(chan struct{}, 1)
	stop, done, execute := t.stop, t.done, t.Execute
	wake := t.wake
	t.access.Unlock()

	go func() {
		defer close(done)
		defer func() {
			t.access.Lock()
			if t.stop == stop && t.running {
				t.running = false
				close(stop)
			}
			t.access.Unlock()
		}()
		if first {
			if err := execute(); err != nil {
				return
			}
		}

		for {
			t.access.Lock()
			interval := t.Interval
			t.access.Unlock()
			timer := time.NewTimer(interval)
			select {
			case <-timer.C:
			case <-stop:
				timer.Stop()
				return
			case <-wake:
				timer.Stop()
				continue
			}
			select {
			case <-stop:
				return
			default:
			}
			if err := execute(); err != nil {
				return
			}
		}
	}()

	return nil
}

func (t *Task) SetInterval(interval time.Duration) {
	if interval > 0 {
		t.access.Lock()
		t.Interval = interval
		if t.running {
			select {
			case t.wake <- struct{}{}:
			default:
			}
		}
		t.access.Unlock()
	}
}

// CloseAndWait must not be called by this task's own Execute function.
func (t *Task) CloseAndWait() {
	t.access.Lock()
	done := t.done
	if t.running {
		t.running = false
		close(t.stop)
	}
	t.access.Unlock()
	if done != nil {
		<-done
	}
}

func (t *Task) Close() {
	t.access.Lock()
	if t.running {
		t.running = false
		close(t.stop)
	}
	t.access.Unlock()
}
