package handlers

import (
	"context"
	"math"
	"testing"
	"time"
)

func TestAIPoolRejectsInvalidCapacity(t *testing.T) {
	for _, tc := range []struct {
		name              string
		workers, capacity int
	}{
		{"zero workers", 0, 1},
		{"negative capacity", 1, -1},
		{"worker limit", 33, 1},
		{"queue limit", 1, 1025},
		{"workers", math.MaxInt, 1},
		{"queue", 1, math.MaxInt},
		{"both", math.MaxInt, math.MaxInt},
	} {
		t.Run(tc.name, func(t *testing.T) {
			defer func() {
				// A runtime makechan panic is not validation: reject before allocation.
				if got := recover(); got != "invalid AI pool configuration" {
					t.Fatalf("expected constructor validation panic, got %v", got)
				}
			}()
			p := NewAIPool(context.Background(), tc.workers, tc.capacity, time.Minute)
			p.Close()
		})
	}
}

func TestAIPoolConfiguredCapacityBounds(t *testing.T) {
	for _, tc := range []struct{ workers, capacity int }{{1, 1}, {4, 64}, {32, 1024}} {
		p := NewAIPool(context.Background(), tc.workers, tc.capacity, time.Minute)
		if cap(p.jobs) != tc.workers+tc.capacity || cap(p.slots) != cap(p.jobs) {
			t.Errorf("incorrect pool capacity for %+v: jobs=%d slots=%d", tc, cap(p.jobs), cap(p.slots))
		}
		p.Close()
	}
}

func waitSignal(t *testing.T, ch <-chan struct{}) {
	t.Helper()
	select {
	case <-ch:
	case <-time.After(2 * time.Second):
		t.Fatal("worker did not progress")
	}
}
func TestAIPoolConcurrencyCapacityCancellation(t *testing.T) {
	p := NewAIPool(context.Background(), 2, 1, time.Minute)
	defer p.Close()
	started := make(chan struct{}, 2)
	for _, u := range []int64{1, 2} {
		j, e := p.Acquire(u)
		if e != nil {
			t.Fatal(e)
		}
		j.Submit(func(ctx context.Context) { started <- struct{}{}; <-ctx.Done() })
	}
	waitSignal(t, started)
	waitSignal(t, started)
	if _, e := p.Acquire(1); e == nil {
		t.Fatal("duplicate active user admitted")
	}
	j, e := p.Acquire(3)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = p.Acquire(4); e == nil {
		t.Fatal("capacity overflow")
	}
	queued := make(chan struct{})
	j.Submit(func(ctx context.Context) { close(queued) })
	p.Cancel(1)
	waitSignal(t, queued)
}
func TestAIPoolTimeoutAndShutdownDrain(t *testing.T) {
	p := NewAIPool(context.Background(), 1, 2, 20*time.Millisecond)
	canceled := make(chan struct{})
	j, _ := p.Acquire(1)
	j.Submit(func(ctx context.Context) { <-ctx.Done(); close(canceled) })
	waitSignal(t, canceled)
	j, _ = p.Acquire(2)
	done := make(chan struct{})
	j.Submit(func(ctx context.Context) { <-ctx.Done(); close(done) })
	p.Close()
	waitSignal(t, done)
	if _, e := p.Acquire(3); e == nil {
		t.Fatal("admitted after shutdown")
	}
}
func TestAIPoolPanicDoesNotKillWorker(t *testing.T) {
	p := NewAIPool(context.Background(), 1, 1, time.Second)
	defer p.Close()
	j, _ := p.Acquire(1)
	j.Submit(func(context.Context) { panic("test") })
	j, _ = p.Acquire(2)
	done := make(chan struct{})
	j.Submit(func(context.Context) { close(done) })
	waitSignal(t, done)
}
