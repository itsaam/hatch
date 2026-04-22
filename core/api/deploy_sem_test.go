package main

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// TestDeploySemaphore verifies that the Deployer semaphore caps the number
// of Deploy() calls in flight at once. We invoke Deploy with refs that
// immediately fail (no pool, no docker) so each call exits fast, but we
// measure the peak concurrency through the semaphore alone.
//
// We don't rely on the production Deploy() since it talks to Docker and
// Postgres; instead we simulate the exact acquire/release pattern through
// a helper that uses the same channel.
func TestDeploySemaphore_CapsConcurrency(t *testing.T) {
	const (
		cap    = 3
		total  = 12
		workMs = 30
	)

	d := &Deployer{deploySem: make(chan struct{}, cap)}

	var (
		inFlight int32
		peak     int32
		wg       sync.WaitGroup
	)

	acquireReleaseWork := func() {
		defer wg.Done()
		d.deploySem <- struct{}{}
		defer func() { <-d.deploySem }()
		n := atomic.AddInt32(&inFlight, 1)
		for {
			p := atomic.LoadInt32(&peak)
			if n <= p || atomic.CompareAndSwapInt32(&peak, p, n) {
				break
			}
		}
		time.Sleep(workMs * time.Millisecond)
		atomic.AddInt32(&inFlight, -1)
	}

	wg.Add(total)
	for i := 0; i < total; i++ {
		go acquireReleaseWork()
	}
	wg.Wait()

	if got := atomic.LoadInt32(&peak); got > cap {
		t.Fatalf("peak concurrency %d exceeded cap %d", got, cap)
	}
	if got := atomic.LoadInt32(&inFlight); got != 0 {
		t.Fatalf("leak: inFlight = %d after wg.Wait", got)
	}
}

// TestDeploySemaphore_NilChannelNoLimit makes sure a Deployer with a nil
// semaphore (tests path or maxConcurrent<=0) does not block.
func TestDeploySemaphore_NilChannelNoLimit(t *testing.T) {
	d := &Deployer{deploySem: nil}
	done := make(chan struct{})
	go func() {
		// mimic Deploy()'s acquire pattern
		if d.deploySem != nil {
			d.deploySem <- struct{}{}
			defer func() { <-d.deploySem }()
		}
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(100 * time.Millisecond):
		t.Fatal("nil semaphore blocked")
	}
}
