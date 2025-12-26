package raft

import (
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/pixperk/lowkey/pkg/fsm"
	"github.com/pixperk/lowkey/pkg/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestConcurrentLockAcquisition(t *testing.T) {
	tmpDir := t.TempDir()

	cfg := &Config{
		NodeID:    uuid.New(),
		BindAddr:  "127.0.0.1:14000",
		DataDir:   tmpDir,
		Bootstrap: true,
	}

	node, err := NewNode(cfg)
	require.NoError(t, err)
	defer node.Shutdown()

	err = node.WaitForLeader(5 * time.Second)
	require.NoError(t, err)

	//create leases for 3 clients
	var leaseIDs []uint64
	for i := 0; i < 3; i++ {
		createResult, err := node.Apply(types.CreateLeaseCmd{
			OwnerID: fmt.Sprintf("client-%d", i),
			TTL:     10 * time.Second,
		})
		require.NoError(t, err)
		createResp := createResult.(fsm.CreateLeaseResponse)
		leaseIDs = append(leaseIDs, createResp.LeaseID)
	}

	//all 3 clients try to acquire the same lock concurrently
	lockName := "contended-lock"
	var wg sync.WaitGroup
	results := make([]struct {
		success      bool
		fencingToken uint64
		err          error
	}, 3)

	for i := 0; i < 3; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			result, err := node.Apply(types.AcquireLockCmd{
				LockName: lockName,
				OwnerID:  fmt.Sprintf("client-%d", idx),
				LeaseID:  leaseIDs[idx],
			})
			if err != nil {
				results[idx].err = err
				return
			}
			results[idx].success = true
			acquireResp := result.(fsm.AcquireLockResponse)
			results[idx].fencingToken = acquireResp.FencingToken
		}(i)
	}

	wg.Wait()

	//verify only one succeeded
	successCount := 0
	var winnerToken uint64
	for _, r := range results {
		if r.success {
			successCount++
			winnerToken = r.fencingToken
		}
	}

	assert.Equal(t, 1, successCount, "only one client should acquire the lock")
	assert.Greater(t, winnerToken, uint64(0), "winner should have a fencing token")

	//verify the other two got ErrLockAlreadyHeld
	failedCount := 0
	for _, r := range results {
		if !r.success && r.err == types.ErrLockAlreadyHeld {
			failedCount++
		}
	}
	assert.Equal(t, 2, failedCount, "two clients should get ErrLockAlreadyHeld")
}

func TestLockWithInvalidLease(t *testing.T) {
	tmpDir := t.TempDir()

	cfg := &Config{
		NodeID:    uuid.New(),
		BindAddr:  "127.0.0.1:15000",
		DataDir:   tmpDir,
		Bootstrap: true,
	}

	node, err := NewNode(cfg)
	require.NoError(t, err)
	defer node.Shutdown()

	err = node.WaitForLeader(5 * time.Second)
	require.NoError(t, err)

	//try to acquire lock with non-existent lease
	_, err = node.Apply(types.AcquireLockCmd{
		LockName: "test-lock",
		OwnerID:  "client-1",
		LeaseID:  999, //non-existent
	})

	require.Error(t, err)
	assert.Equal(t, types.ErrLeaseNotFound, err)
}

func TestLockWithExpiredLease(t *testing.T) {
	tmpDir := t.TempDir()

	cfg := &Config{
		NodeID:    uuid.New(),
		BindAddr:  "127.0.0.1:16000",
		DataDir:   tmpDir,
		Bootstrap: true,
	}

	node, err := NewNode(cfg)
	require.NoError(t, err)
	defer node.Shutdown()

	err = node.WaitForLeader(5 * time.Second)
	require.NoError(t, err)

	//create lease with very short TTL
	createResult, err := node.Apply(types.CreateLeaseCmd{
		OwnerID: "client-1",
		TTL:     200 * time.Millisecond,
	})
	require.NoError(t, err)

	createResp := createResult.(fsm.CreateLeaseResponse)
	leaseID := createResp.LeaseID

	//wait for lease to expire
	time.Sleep(400 * time.Millisecond)

	//try to acquire lock with expired lease
	_, err = node.Apply(types.AcquireLockCmd{
		LockName: "test-lock",
		OwnerID:  "client-1",
		LeaseID:  leaseID,
	})

	require.Error(t, err)
	//could be ErrLeaseExpired (if checked before deletion) or ErrLeaseNotFound (if expiry loop already deleted it)
	assert.True(t,
		err == types.ErrLeaseExpired || err == types.ErrLeaseNotFound,
		"should return ErrLeaseExpired or ErrLeaseNotFound, got: %v", err)
}

func TestFencingTokenMonotonicity(t *testing.T) {
	tmpDir := t.TempDir()

	cfg := &Config{
		NodeID:    uuid.New(),
		BindAddr:  "127.0.0.1:17000",
		DataDir:   tmpDir,
		Bootstrap: true,
	}

	node, err := NewNode(cfg)
	require.NoError(t, err)
	defer node.Shutdown()

	err = node.WaitForLeader(5 * time.Second)
	require.NoError(t, err)

	//create lease
	createResult, err := node.Apply(types.CreateLeaseCmd{
		OwnerID: "client-1",
		TTL:     10 * time.Second,
	})
	require.NoError(t, err)
	createResp := createResult.(fsm.CreateLeaseResponse)
	leaseID := createResp.LeaseID

	//acquire and release locks multiple times, collecting fencing tokens
	var tokens []uint64
	for i := 0; i < 5; i++ {
		lockName := fmt.Sprintf("lock-%d", i)

		//acquire
		acquireResult, err := node.Apply(types.AcquireLockCmd{
			LockName: lockName,
			OwnerID:  "client-1",
			LeaseID:  leaseID,
		})
		require.NoError(t, err)

		acquireResp := acquireResult.(fsm.AcquireLockResponse)
		tokens = append(tokens, acquireResp.FencingToken)

		//release
		_, err = node.Apply(types.ReleaseLockCmd{
			LockName: lockName,
			LeaseID:  leaseID,
		})
		require.NoError(t, err)
	}

	//verify tokens are strictly monotonically increasing
	for i := 1; i < len(tokens); i++ {
		assert.Greater(t, tokens[i], tokens[i-1],
			"fencing token %d should be greater than token %d", tokens[i], tokens[i-1])
	}

	//verify each token is unique
	seen := make(map[uint64]bool)
	for _, token := range tokens {
		assert.False(t, seen[token], "fencing token %d should be unique", token)
		seen[token] = true
	}
}

func TestLockReleaseByNonOwner(t *testing.T) {
	tmpDir := t.TempDir()

	cfg := &Config{
		NodeID:    uuid.New(),
		BindAddr:  "127.0.0.1:18000",
		DataDir:   tmpDir,
		Bootstrap: true,
	}

	node, err := NewNode(cfg)
	require.NoError(t, err)
	defer node.Shutdown()

	err = node.WaitForLeader(5 * time.Second)
	require.NoError(t, err)

	//create two leases for two different clients
	createResult1, err := node.Apply(types.CreateLeaseCmd{
		OwnerID: "client-1",
		TTL:     10 * time.Second,
	})
	require.NoError(t, err)
	lease1 := createResult1.(fsm.CreateLeaseResponse).LeaseID

	createResult2, err := node.Apply(types.CreateLeaseCmd{
		OwnerID: "client-2",
		TTL:     10 * time.Second,
	})
	require.NoError(t, err)
	lease2 := createResult2.(fsm.CreateLeaseResponse).LeaseID

	//client-1 acquires lock
	_, err = node.Apply(types.AcquireLockCmd{
		LockName: "shared-lock",
		OwnerID:  "client-1",
		LeaseID:  lease1,
	})
	require.NoError(t, err)

	//client-2 tries to release lock (should fail - not the owner)
	_, err = node.Apply(types.ReleaseLockCmd{
		LockName: "shared-lock",
		LeaseID:  lease2,
	})

	require.Error(t, err)
	assert.Equal(t, types.ErrNotLockOwner, err)

	//verify lock is still held by client-1
	lock, exists := node.fsm.GetLock("shared-lock")
	assert.True(t, exists, "lock should still exist")
	assert.Equal(t, lease1, lock.LeaseID, "lock should still be owned by client-1")
}

func TestLockReacquisition(t *testing.T) {
	tmpDir := t.TempDir()

	cfg := &Config{
		NodeID:    uuid.New(),
		BindAddr:  "127.0.0.1:19000",
		DataDir:   tmpDir,
		Bootstrap: true,
	}

	node, err := NewNode(cfg)
	require.NoError(t, err)
	defer node.Shutdown()

	err = node.WaitForLeader(5 * time.Second)
	require.NoError(t, err)

	//create lease
	createResult, err := node.Apply(types.CreateLeaseCmd{
		OwnerID: "client-1",
		TTL:     10 * time.Second,
	})
	require.NoError(t, err)
	leaseID := createResult.(fsm.CreateLeaseResponse).LeaseID

	//acquire lock
	acquireResult1, err := node.Apply(types.AcquireLockCmd{
		LockName: "my-lock",
		OwnerID:  "client-1",
		LeaseID:  leaseID,
	})
	require.NoError(t, err)
	token1 := acquireResult1.(fsm.AcquireLockResponse).FencingToken

	//try to acquire same lock again with same lease (should be idempotent)
	acquireResult2, err := node.Apply(types.AcquireLockCmd{
		LockName: "my-lock",
		OwnerID:  "client-1",
		LeaseID:  leaseID,
	})
	require.NoError(t, err)
	token2 := acquireResult2.(fsm.AcquireLockResponse).FencingToken

	//should return same fencing token (idempotent)
	assert.Equal(t, token1, token2, "reacquiring lock should return same fencing token")
}

func TestStaleClientScenario(t *testing.T) {
	tmpDir := t.TempDir()

	cfg := &Config{
		NodeID:    uuid.New(),
		BindAddr:  "127.0.0.1:20000",
		DataDir:   tmpDir,
		Bootstrap: true,
	}

	node, err := NewNode(cfg)
	require.NoError(t, err)
	defer node.Shutdown()

	err = node.WaitForLeader(5 * time.Second)
	require.NoError(t, err)

	//simulate protected resource that validates fencing tokens
	type ProtectedResource struct {
		mu            sync.Mutex
		lastSeenToken uint64
		data          string
	}

	resource := &ProtectedResource{}

	writeToResource := func(data string, token uint64) error {
		resource.mu.Lock()
		defer resource.mu.Unlock()

		if token < resource.lastSeenToken {
			return fmt.Errorf("stale token: %d < %d", token, resource.lastSeenToken)
		}

		resource.data = data
		resource.lastSeenToken = token
		return nil
	}

	//client A creates lease and acquires lock
	createResultA, err := node.Apply(types.CreateLeaseCmd{
		OwnerID: "client-a",
		TTL:     200 * time.Millisecond,
	})
	require.NoError(t, err)
	leaseA := createResultA.(fsm.CreateLeaseResponse).LeaseID

	acquireResultA, err := node.Apply(types.AcquireLockCmd{
		LockName: "resource-lock",
		OwnerID:  "client-a",
		LeaseID:  leaseA,
	})
	require.NoError(t, err)
	tokenA := acquireResultA.(fsm.AcquireLockResponse).FencingToken

	//client A writes with its token
	err = writeToResource("data-from-A", tokenA)
	require.NoError(t, err)
	assert.Equal(t, "data-from-A", resource.data)
	assert.Equal(t, tokenA, resource.lastSeenToken)

	//simulate client A pausing (GC pause, network issue, etc.)
	//wait for lease to expire
	time.Sleep(400 * time.Millisecond)

	//verify lease expired and lock released
	statsAfterExpiry := node.Stats()
	assert.Equal(t, 0, statsAfterExpiry.Locks, "lock should be released after lease expiry")

	//client B creates lease and acquires lock
	createResultB, err := node.Apply(types.CreateLeaseCmd{
		OwnerID: "client-b",
		TTL:     10 * time.Second,
	})
	require.NoError(t, err)
	leaseB := createResultB.(fsm.CreateLeaseResponse).LeaseID

	acquireResultB, err := node.Apply(types.AcquireLockCmd{
		LockName: "resource-lock",
		OwnerID:  "client-b",
		LeaseID:  leaseB,
	})
	require.NoError(t, err)
	tokenB := acquireResultB.(fsm.AcquireLockResponse).FencingToken

	//verify token B is greater than token A
	assert.Greater(t, tokenB, tokenA, "new lock acquisition should have higher fencing token")

	//client B writes with its token
	err = writeToResource("data-from-B", tokenB)
	require.NoError(t, err)
	assert.Equal(t, "data-from-B", resource.data)
	assert.Equal(t, tokenB, resource.lastSeenToken)

	//client A wakes up and tries to write with stale token
	err = writeToResource("data-from-A-stale", tokenA)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "stale token")

	//verify resource still has client B's data
	assert.Equal(t, "data-from-B", resource.data, "stale write should not modify data")
	assert.Equal(t, tokenB, resource.lastSeenToken, "last seen token should not be downgraded")
}
