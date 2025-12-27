package client

import (
	"context"
	"fmt"
	"log"
	"sync"
	"time"

	pb "github.com/pixperk/lowkey/api/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

type Client struct {
	addr    string
	ownerID string
	conn    *grpc.ClientConn
	client  pb.LockServiceClient

	leaseID   uint64
	leaseTTL  time.Duration
	heartbeat pb.LockService_HeartbeatClient

	mu     sync.Mutex
	stopCh chan struct{}
}

func NewClient(addr, ownerID string) (*Client, error) {
	conn, err := grpc.NewClient(addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return nil, fmt.Errorf("failed to connect: %w", err)
	}

	return &Client{
		addr:    addr,
		ownerID: ownerID,
		conn:    conn,
		client:  pb.NewLockServiceClient(conn),
		stopCh:  make(chan struct{}),
	}, nil
}

func (c *Client) Start(ctx context.Context, ttl time.Duration) error {
	resp, err := c.client.CreateLease(ctx, &pb.CreateLeaseRequest{
		OwnerId:    c.ownerID,
		TtlSeconds: int64(ttl.Seconds()),
	})
	if err != nil {
		return fmt.Errorf("create lease: %w", err)
	}

	c.mu.Lock()
	c.leaseID = resp.LeaseId
	c.leaseTTL = ttl
	c.mu.Unlock()

	stream, err := c.client.Heartbeat(ctx)
	if err != nil {
		return fmt.Errorf("heartbeat stream: %w", err)
	}

	c.mu.Lock()
	c.heartbeat = stream
	c.mu.Unlock()

	go c.heartbeatLoop(ctx)

	return nil
}

func (c *Client) heartbeatLoop(ctx context.Context) {
	ticker := time.NewTicker(c.leaseTTL / 3)
	defer ticker.Stop()

	var failureCount int

	for {
		select {
		case <-ticker.C:
			c.mu.Lock()
			leaseID := c.leaseID
			stream := c.heartbeat
			c.mu.Unlock()

			if stream == nil {
				continue
			}

			if err := stream.Send(&pb.HeartbeatRequest{LeaseId: leaseID}); err != nil {
				failureCount++
				log.Printf("[WARNING] Heartbeat send failed (attempt %d): %v", failureCount, err)
				if failureCount >= 2 {
					log.Printf("[CRITICAL] Lease %d may expire soon - heartbeat failing", leaseID)
				}
				continue
			}

			if _, err := stream.Recv(); err != nil {
				failureCount++
				log.Printf("[WARNING] Heartbeat recv failed (attempt %d): %v", failureCount, err)
				if failureCount >= 2 {
					log.Printf("[CRITICAL] Lease %d may expire soon - heartbeat failing", leaseID)
				}
				continue
			}

			// Reset failure count on success
			if failureCount > 0 {
				log.Printf("[INFO] Heartbeat recovered after %d failures", failureCount)
				failureCount = 0
			}

		case <-c.stopCh:
			return
		case <-ctx.Done():
			return
		}
	}
}

func (c *Client) Acquire(ctx context.Context, lockName string) (*Lock, error) {
	c.mu.Lock()
	leaseID := c.leaseID
	c.mu.Unlock()

	resp, err := c.client.AcquireLock(ctx, &pb.AcquireLockRequest{
		LockName: lockName,
		OwnerId:  c.ownerID,
		LeaseId:  leaseID,
	})
	if err != nil {
		return nil, fmt.Errorf("acquire lock: %w", err)
	}

	return &Lock{
		client:       c,
		name:         lockName,
		fencingToken: resp.FencingToken,
	}, nil
}

func (c *Client) Release(ctx context.Context, lockName string) error {
	c.mu.Lock()
	leaseID := c.leaseID
	c.mu.Unlock()

	_, err := c.client.ReleaseLock(ctx, &pb.ReleaseLockRequest{
		LockName: lockName,
		LeaseId:  leaseID,
	})
	if err != nil {
		return fmt.Errorf("release lock: %w", err)
	}

	return nil
}

func (c *Client) Status(ctx context.Context) (*pb.GetStatusResponse, error) {
	return c.client.GetStatus(ctx, &pb.GetStatusRequest{})
}

func (c *Client) Stop() error {
	close(c.stopCh)

	c.mu.Lock()
	if c.heartbeat != nil {
		c.heartbeat.CloseSend()
	}
	c.mu.Unlock()

	if c.conn != nil {
		return c.conn.Close()
	}

	return nil
}
