package store

import (
	"fmt"
	"math/rand"
	"sync"
	"testing"
	"time"
)

func TestMemoryStore_ConcurrentMixedOps(t *testing.T) {
	s := NewMemoryStore()

	// Make randomness stable for reproducible test runs.
	rand.Seed(42)

	const (
		workers  = 50
		opsPerW  = 2000
		keySpace = 200
	)

	var wg sync.WaitGroup
	wg.Add(workers)

	for w := 0; w < workers; w++ {
		go func(workerID int) {
			defer wg.Done()

			for i := 0; i < opsPerW; i++ {
				k := fmt.Sprintf("k-%d", rand.Intn(keySpace))
				op := rand.Intn(3)

				switch op {
				case 0: // Set
					_ = s.Set(k, fmt.Sprintf("v-%d-%d", workerID, i))
				case 1: // Get
					_, err := s.Get(k)
					if err != nil && err != ErrKeyNotFound {
						t.Errorf("unexpected Get error: %v", err)
						return
					}
				case 2: // Delete
					err := s.Delete(k)
					if err != nil && err != ErrKeyNotFound {
						t.Errorf("unexpected Delete error: %v", err)
						return
					}
				}
			}
		}(w)
	}

	wg.Wait()
}

// This test is designed to heavily hit reads concurrently.
// It should be fast and should not trigger race detector issues.
func TestMemoryStore_ConcurrentReads(t *testing.T) {
	s := NewMemoryStore()

	// Seed some data
	for i := 0; i < 1000; i++ {
		_ = s.Set(fmt.Sprintf("k-%d", i), "value")
	}

	const readers = 100
	const readsPer = 3000

	var wg sync.WaitGroup
	wg.Add(readers)

	for r := 0; r < readers; r++ {
		go func() {
			defer wg.Done()
			for i := 0; i < readsPer; i++ {
				_, err := s.Get(fmt.Sprintf("k-%d", rand.Intn(1000)))
				if err != nil {
					t.Errorf("expected value, got err=%v", err)
					return
				}
			}
		}()
	}

	wg.Wait()
}

// Optional: a timeout guard so your test suite never hangs silently.
func TestMemoryStore_NoDeadlock(t *testing.T) {
	done := make(chan struct{})

	go func() {
		defer close(done)
		s := NewMemoryStore()
		var wg sync.WaitGroup

		for i := 0; i < 100; i++ {
			wg.Add(1)
			go func(i int) {
				defer wg.Done()
				key := fmt.Sprintf("k-%d", i%10)
				_ = s.Set(key, "x")
				_, _ = s.Get(key)
				_ = s.Delete(key)
			}(i)
		}

		wg.Wait()
	}()

	select {
	case <-done:
		// ok
	case <-time.After(2 * time.Second):
		t.Fatal("test timed out - possible deadlock")
	}
}
