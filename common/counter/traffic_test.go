package counter

import (
	"net"
	"sync"
	"testing"
)

func TestConcurrentDrainPreservesBytes(t *testing.T) {
	var storage TrafficStorage
	var writers sync.WaitGroup
	for range 4 {
		writers.Add(1)
		go func() {
			defer writers.Done()
			for range 10000 {
				storage.UpCounter.Add(1)
				storage.DownCounter.Add(2)
			}
		}()
	}
	done := make(chan struct{})
	go func() { writers.Wait(); close(done) }()
	var up, down int64
	for {
		u, d := storage.Snapshot(true)
		up += u
		down += d
		select {
		case <-done:
			u, d = storage.Snapshot(true)
			up += u
			down += d
			if up != 40000 || down != 80000 {
				t.Fatalf("lost bytes: %d/%d", up, down)
			}
			return
		default:
		}
	}
}

func TestConnectionCountsAccumulateAcrossReads(t *testing.T) {
	left, right := net.Pipe()
	defer left.Close()
	defer right.Close()
	var storage TrafficStorage
	conn := NewConnCounter(left, &storage)
	go func() { _, _ = right.Write([]byte("abc")); _, _ = right.Write([]byte("defgh")) }()
	buffer := make([]byte, 10)
	for range 2 {
		if _, err := conn.Read(buffer); err != nil {
			t.Fatal(err)
		}
	}
	up, _ := storage.Snapshot(false)
	if up != 8 {
		t.Fatalf("expected cumulative bytes, got %d", up)
	}
}
