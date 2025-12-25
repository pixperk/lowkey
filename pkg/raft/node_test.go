package raft

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/pixperk/lowkey/pkg/fsm"
	"github.com/pixperk/lowkey/pkg/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestSingleNodeSmoke tests basic Raft functionality with a single node
func TestSingleNodeSmoke(t *testing.T) {
	tmpDir := t.TempDir()

	// Create config for single node
	cfg := &Config{
		NodeID:    uuid.New(),
		BindAddr:  "127.0.0.1:0", // 0 = pick random available port
		DataDir:   tmpDir,
		Bootstrap: true, // First node in cluster
	}

	// Create node
	node, err := NewNode(cfg)
	require.NoError(t, err, "failed to create node")
	defer node.Shutdown()

	//wait for leader election
	err = node.WaitForLeader(5 * time.Second)
	require.NoError(t, err, "no leader elected")
	assert.True(t, node.IsLeader(), "single node should be leader")

	// Test 1: Create a lease
	createResult, err := node.Apply(types.CreateLeaseCmd{
		OwnerID: "client-1",
		TTL:     10 * time.Second,
	})
	require.NoError(t, err, "failed to create lease")

	// Verify response type
	createResp, ok := createResult.(fsm.CreateLeaseResponse)
	require.True(t, ok, "expected CreateLeaseResponse")
	assert.NotZero(t, createResp.LeaseID, "lease ID should not be zero")

	//Test 2 : Acquire a lock with the created lease
	acquireResult, err := node.Apply(types.AcquireLockCmd{
		LockName: "my-lock",
		OwnerID:  "client-1",
		LeaseID:  createResp.LeaseID,
	})

	require.NoError(t, err, "failed to acquire lock")

	//Verify lock response
	acquireResp, ok := acquireResult.(fsm.AcquireLockResponse)
	require.True(t, ok, "expected AcquireLockResponse")
	assert.Equal(t, uint64(1), acquireResp.FencingToken, "first lock should have token 1")

	// Test 3: Check FSM stats
	stats := node.Stats()
	assert.Equal(t, 1, stats.Leases, "should have 1 lease")
	assert.Equal(t, 1, stats.Locks, "should have 1 lock")

	//Test4 : Release the lock
	releaseResult, err := node.Apply(types.ReleaseLockCmd{
		LockName: "my-lock",
		LeaseID:  createResp.LeaseID,
	})
	require.NoError(t, err, "failed to release lock")

	//Verify release response
	releaseResp, ok := releaseResult.(fsm.ReleaseLockResponse)
	require.True(t, ok, "expected ReleaseLockResponse")
	assert.True(t, releaseResp.Released, "lock release should be successful")

	stats = node.Stats()
	assert.Equal(t, 1, stats.Leases, "should still have 1 lease")
	assert.Equal(t, 0, stats.Locks, "should have 0 locks after release")
}
