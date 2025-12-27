package client_test

import (
	"context"
	"fmt"
	"sort"
	"sync"
	"testing"
	"time"

	"github.com/pixperk/lowkey/pkg/client"
)

// Run with: go test -run=Percentile -v ./pkg/client/

type latencyStats struct {
	samples []time.Duration
	mu      sync.Mutex
}

func (s *latencyStats) record(d time.Duration) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.samples = append(s.samples, d)
}

func (s *latencyStats) calculate() map[string]time.Duration {
	s.mu.Lock()
	defer s.mu.Unlock()

	if len(s.samples) == 0 {
		return nil
	}

	sort.Slice(s.samples, func(i, j int) bool {
		return s.samples[i] < s.samples[j]
	})

	percentile := func(p float64) time.Duration {
		idx := int(float64(len(s.samples)) * p)
		if idx >= len(s.samples) {
			idx = len(s.samples) - 1
		}
		return s.samples[idx]
	}

	return map[string]time.Duration{
		"min":   s.samples[0],
		"p50":   percentile(0.50),
		"p90":   percentile(0.90),
		"p95":   percentile(0.95),
		"p99":   percentile(0.99),
		"p99.9": percentile(0.999),
		"max":   s.samples[len(s.samples)-1],
	}
}

func TestPercentileSequential(t *testing.T) {
	c, err := client.NewClient(serverAddr, "percentile-sequential")
	if err != nil {
		t.Fatalf("Failed to connect: %v", err)
	}
	defer c.Stop()

	ctx := context.Background()
	if err := c.Start(ctx, 10*time.Second); err != nil {
		t.Fatalf("Failed to start: %v", err)
	}

	lockName := "percentile-lock-sequential"
	stats := &latencyStats{}

	iterations := 1000
	t.Logf("Running %d sequential lock operations...", iterations)

	for i := 0; i < iterations; i++ {
		start := time.Now()
		lock, err := c.Acquire(ctx, lockName)
		if err != nil {
			t.Fatalf("Failed to acquire: %v", err)
		}
		lock.Release(ctx)
		stats.record(time.Since(start))
	}

	printStats(t, "Sequential", stats)
}

func TestPercentileParallel(t *testing.T) {
	const numClients = 3
	iterations := 1000
	stats := &latencyStats{}

	t.Logf("Running %d parallel lock operations (%d clients)...", iterations, numClients)

	var wg sync.WaitGroup
	for i := 0; i < numClients; i++ {
		wg.Add(1)
		go func(clientID int) {
			defer wg.Done()

			c, err := client.NewClient(serverAddr, fmt.Sprintf("percentile-parallel-%d", clientID))
			if err != nil {
				t.Errorf("Failed to connect: %v", err)
				return
			}
			defer c.Stop()

			ctx := context.Background()
			if err := c.Start(ctx, 10*time.Second); err != nil {
				t.Errorf("Failed to start: %v", err)
				return
			}

			lockName := fmt.Sprintf("lock-parallel-%d", clientID)

			for j := 0; j < iterations/numClients; j++ {
				start := time.Now()
				lock, err := c.Acquire(ctx, lockName)
				if err != nil {
					continue
				}
				lock.Release(ctx)
				stats.record(time.Since(start))
			}
		}(i)
	}

	wg.Wait()
	printStats(t, "Parallel", stats)
}

func TestPercentileContention(t *testing.T) {
	const numClients = 3
	lockName := "percentile-lock-contention"
	iterations := 300
	stats := &latencyStats{}

	clients := make([]*client.Client, numClients)
	ctx := context.Background()

	for i := 0; i < numClients; i++ {
		c, err := client.NewClient(serverAddr, fmt.Sprintf("percentile-contention-%d", i))
		if err != nil {
			t.Fatalf("Failed to connect: %v", err)
		}
		defer c.Stop()

		if err := c.Start(ctx, 10*time.Second); err != nil {
			t.Fatalf("Failed to start: %v", err)
		}
		clients[i] = c
	}

	t.Logf("Running %d contention operations (%d clients competing)...", iterations, numClients)

	var wg sync.WaitGroup
	opsPerClient := iterations / numClients

	for i := 0; i < numClients; i++ {
		wg.Add(1)
		go func(c *client.Client) {
			defer wg.Done()
			for j := 0; j < opsPerClient; j++ {
				start := time.Now()
				lock, err := c.Acquire(ctx, lockName)
				if err != nil {
					continue
				}
				time.Sleep(1 * time.Millisecond) // Simulate work
				lock.Release(ctx)
				stats.record(time.Since(start))
			}
		}(clients[i])
	}

	wg.Wait()
	printStats(t, "Contention", stats)
}

func printStats(t *testing.T, name string, stats *latencyStats) {
	percentiles := stats.calculate()
	if percentiles == nil {
		t.Logf("No data collected for %s", name)
		return
	}

	t.Logf("\n=== %s Latency Percentiles ===", name)
	t.Logf("  Samples: %d", len(stats.samples))
	t.Logf("  Min:     %v", percentiles["min"])
	t.Logf("  p50:     %v", percentiles["p50"])
	t.Logf("  p90:     %v", percentiles["p90"])
	t.Logf("  p95:     %v", percentiles["p95"])
	t.Logf("  p99:     %v", percentiles["p99"])
	t.Logf("  p99.9:   %v", percentiles["p99.9"])
	t.Logf("  Max:     %v", percentiles["max"])
	t.Logf("")
}
