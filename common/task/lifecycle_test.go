package task

import (
	"errors"
	"sync/atomic"
	"testing"
	"time"
)

func TestStoppedGenerationCannotStopRestartedTask(t *testing.T) {
	entered, release, restarted := make(chan struct{}), make(chan struct{}), make(chan struct{})
	var calls atomic.Int32
	task := &Task{Interval: time.Hour, Execute: func() error {
		if calls.Add(1) == 1 {
			close(entered)
			<-release
			return errors.New("old generation")
		}
		close(restarted)
		return nil
	}}
	if err := task.Start(true); err != nil {
		t.Fatal(err)
	}
	<-entered
	task.Close()
	if err := task.Start(true); err != nil {
		t.Fatal(err)
	}
	<-restarted
	close(release)
	defer task.CloseAndWait()
	time.Sleep(20 * time.Millisecond)
	task.access.Lock()
	running := task.running
	task.access.Unlock()
	if !running {
		t.Fatal("old generation stopped restarted task")
	}
}

func TestCloseAndWaitWaitsForCallback(t *testing.T) {
	entered, release, done := make(chan struct{}), make(chan struct{}), make(chan struct{})
	task := &Task{Interval: time.Hour, Execute: func() error { close(entered); <-release; return nil }}
	if err := task.Start(true); err != nil {
		t.Fatal(err)
	}
	<-entered
	go func() { task.CloseAndWait(); close(done) }()
	select {
	case <-done:
		t.Fatal("returned while callback was running")
	case <-time.After(20 * time.Millisecond):
	}
	close(release)
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("failed to stop")
	}
}

func TestIntervalChangeWakesPendingTimer(t *testing.T) {
	called := make(chan struct{}, 1)
	task := &Task{Interval: time.Hour, Execute: func() error {
		select {
		case called <- struct{}{}:
		default:
		}
		return nil
	}}
	if err := task.Start(false); err != nil {
		t.Fatal(err)
	}
	defer task.CloseAndWait()
	task.SetInterval(10 * time.Millisecond)
	select {
	case <-called:
	case <-time.After(time.Second):
		t.Fatal("interval update did not wake pending timer")
	}
}
