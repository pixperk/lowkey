package client_test

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/pixperk/lowkey/pkg/client"
)

// Run with: go test -bench=. -benchtime=10s ./pkg/client/

const serverAddr = "localhost:9000"

func BenchmarkSequential(b *testing.B) {
	c, err := client.NewClient(serverAddr, "bench-sequential")
	if err != nil {
		b.Fatalf("Failed to connect: %v", err)
	}
	defer c.Stop()

	ctx := context.Background()
	if err := c.Start(ctx, 10*time.Second); err != nil {
		b.Fatalf("Failed to start: %v", err)
	}

	lockName := "bench-lock-sequential"

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		lock, err := c.Acquire(ctx, lockName)
		if err != nil {
			b.Fatalf("Failed to acquire: %v", err)
		}
		lock.Release(ctx)
	}
}

func BenchmarkParallel(b *testing.B) {
	const numClients = 3

	b.RunParallel(func(pb *testing.PB) {
		c, err := client.NewClient(serverAddr, fmt.Sprintf("bench-parallel-%d", time.Now().UnixNano()))
		if err != nil {
			b.Fatalf("Failed to connect: %v", err)
		}
		defer c.Stop()

		ctx := context.Background()
		if err := c.Start(ctx, 10*time.Second); err != nil {
			b.Fatalf("Failed to start: %v", err)
		}

		lockName := fmt.Sprintf("lock-%d", time.Now().UnixNano())

		for pb.Next() {
			lock, err := c.Acquire(ctx, lockName)
			if err != nil {
				continue
			}
			lock.Release(ctx)
		}
	})
}

func BenchmarkContention(b *testing.B) {
	const numClients = 3
	lockName := "bench-lock-contention"

	clients := make([]*client.Client, numClients)
	ctx := context.Background()

	for i := 0; i < numClients; i++ {
		c, err := client.NewClient(serverAddr, fmt.Sprintf("bench-contention-%d", i))
		if err != nil {
			b.Fatalf("Failed to connect: %v", err)
		}
		defer c.Stop()

		if err := c.Start(ctx, 10*time.Second); err != nil {
			b.Fatalf("Failed to start: %v", err)
		}
		clients[i] = c
	}

	b.ResetTimer()

	var wg sync.WaitGroup
	opsPerClient := b.N / numClients

	for i := 0; i < numClients; i++ {
		wg.Add(1)
		go func(c *client.Client) {
			defer wg.Done()
			for j := 0; j < opsPerClient; j++ {
				lock, err := c.Acquire(ctx, lockName)
				if err != nil {
					continue
				}
				time.Sleep(1 * time.Millisecond)
				lock.Release(ctx)
			}
		}(clients[i])
	}

	wg.Wait()
}
