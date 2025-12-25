package raft

import (
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/hashicorp/raft"
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

func TestStatePersistance(t *testing.T) {
	tmpDir := t.TempDir()

	nodeID := uuid.New()

	cfg := &Config{
		NodeID:    nodeID,
		BindAddr:  "127.0.0.1:7000", // fixed port for restart
		DataDir:   tmpDir,
		Bootstrap: true,
	}

	node1, err := NewNode(cfg)
	require.NoError(t, err, "failed to create node1")

	err = node1.WaitForLeader(5 * time.Second)
	require.NoError(t, err)

	//some commands to change state
	createResult, err := node1.Apply(types.CreateLeaseCmd{
		OwnerID: "client-1",
		TTL:     10 * time.Second,
	})
	require.NoError(t, err)

	createResp, ok := createResult.(fsm.CreateLeaseResponse)
	require.True(t, ok)
	leaseId := createResp.LeaseID

	acquireResult, err := node1.Apply(types.AcquireLockCmd{
		LockName: "my-lock",
		OwnerID:  "client-1",
		LeaseID:  leaseId,
	})
	require.NoError(t, err)

	acquireResp, ok := acquireResult.(fsm.AcquireLockResponse)
	require.True(t, ok)
	assert.Equal(t, uint64(1), acquireResp.FencingToken)

	originalToken := acquireResp.FencingToken

	//stats before shutdown
	statsBefore := node1.Stats()
	assert.Equal(t, 1, statsBefore.Leases)
	assert.Equal(t, 1, statsBefore.Locks)

	//shutdown node
	err = node1.Shutdown()
	require.NoError(t, err, "failed to shutdown node1")

	time.Sleep(500 * time.Millisecond)

	//modify config to not bootstrap
	cfg.Bootstrap = false

	//restart node
	node2, err := NewNode(cfg)
	require.NoError(t, err, "failed to recreate node")
	defer node2.Shutdown()

	err = node2.WaitForLeader(5 * time.Second)
	require.NoError(t, err)

	//verify state after restart
	statsAfter := node2.Stats()
	assert.Equal(t, statsBefore.Leases, statsAfter.Leases, "leases count should persist after restart")
	assert.Equal(t, statsBefore.Locks, statsAfter.Locks, "locks count should persist after restart")

	//try to acquire the same lock again, should get next fencing token
	acquireResult2, err := node2.Apply(types.AcquireLockCmd{
		LockName: "another-lock",
		OwnerID:  "client-1",
		LeaseID:  leaseId,
	})
	require.NoError(t, err)

	acquireResp2, ok := acquireResult2.(fsm.AcquireLockResponse)
	require.True(t, ok)
	assert.Equal(t, originalToken+1, acquireResp2.FencingToken, "fencing token should increment after restart")
}

func TestMultiNodeCluster(t *testing.T) {
	//create 3 node cluster
	nodes := make([]*Node, 3)
	cfgs := make([]*Config, 3)

	for i := 0; i < 3; i++ {
		cfgs[i] = &Config{
			NodeID:    uuid.New(),
			BindAddr:  fmt.Sprintf("127.0.0.1:%d", 8000+i),
			DataDir:   filepath.Join(t.TempDir(), fmt.Sprintf("node%d", i)),
			Bootstrap: i == 0, //bootstrap only first node
		}
	}

	var err error
	nodes[0], err = NewNode(cfgs[0])
	require.NoError(t, err, "failed to create node 0")
	defer nodes[0].Shutdown()

	err = nodes[0].WaitForLeader(5 * time.Second)
	require.NoError(t, err, "no leader elected in cluster")
	require.True(t, nodes[0].IsLeader(), "node 0 should be leader")

	for i := 1; i < 3; i++ {
		nodes[i], err = NewNode(cfgs[i])
		require.NoError(t, err, fmt.Sprintf("failed to create node %d", i))
		defer nodes[i].Shutdown()

		future := nodes[0].raft.AddVoter(
			raft.ServerID(cfgs[i].NodeID.String()),
			raft.ServerAddress(cfgs[i].BindAddr),
			0, 0,
		)
		require.NoError(t, future.Error(), fmt.Sprintf("failed to add node %d as voter", i))

	}

	time.Sleep(2 * time.Second)

	var leader *Node
	leaderCnt := 0

	for _, node := range nodes {
		if node.IsLeader() {
			leader = node
			leaderCnt++
		}
	}

	require.Equal(t, 1, leaderCnt, "there should be exactly one leader")
	require.NotNil(t, leader, "leader node should not be nil")

	//apply command via leader
	createResult, err := leader.Apply(types.CreateLeaseCmd{
		OwnerID: "client-1",
		TTL:     10 * time.Second,
	})
	require.NoError(t, err, "failed to create lease via leader")

	createResp, ok := createResult.(fsm.CreateLeaseResponse)
	require.True(t, ok, "expected CreateLeaseResponse from leader")
	assert.NotZero(t, createResp.LeaseID, "lease ID should not be zero from leader")

	acquireResult, err := leader.Apply(types.AcquireLockCmd{
		LockName: "cluster-lock",
		OwnerID:  "client-1",
		LeaseID:  createResp.LeaseID,
	})
	require.NoError(t, err, "failed to acquire lock via leader")

	acquireResp, ok := acquireResult.(fsm.AcquireLockResponse)
	require.True(t, ok, "expected AcquireLockResponse from leader")
	assert.Equal(t, uint64(1), acquireResp.FencingToken, "first lock should have token 1 from leader")

	time.Sleep(1 * time.Second)

	//all nodes should have the lease and lock
	for i, node := range nodes {
		stats := node.Stats()
		assert.Equal(t, 1, stats.Leases, fmt.Sprintf("node %d should have 1 lease", i))
		assert.Equal(t, 1, stats.Locks, fmt.Sprintf("node %d should have 1 lock", i))
	}
}

func TestLeaderElection(t *testing.T) {
	nodes := make([]*Node, 3)
	cfgs := make([]*Config, 3)

	for i := 0; i < 3; i++ {
		cfgs[i] = &Config{
			NodeID:    uuid.New(),
			BindAddr:  fmt.Sprintf("127.0.0.1:%d", 9000+i),
			DataDir:   filepath.Join(t.TempDir(), fmt.Sprintf("node%d", i)),
			Bootstrap: i == 0, //bootstrap only first node
		}
	}

	var err error
	nodes[0], err = NewNode(cfgs[0])
	require.NoError(t, err, "failed to create node 0")
	defer nodes[0].Shutdown()

	err = nodes[0].WaitForLeader(5 * time.Second)
	require.NoError(t, err, "no leader elected in cluster")
	require.True(t, nodes[0].IsLeader(), "node 0 should be leader")

	for i := 1; i < 3; i++ {
		nodes[i], err = NewNode(cfgs[i])
		require.NoError(t, err, fmt.Sprintf("failed to create node %d", i))
		defer nodes[i].Shutdown()

		future := nodes[0].raft.AddVoter(
			raft.ServerID(cfgs[i].NodeID.String()),
			raft.ServerAddress(cfgs[i].BindAddr),
			0, 0,
		)
		require.NoError(t, future.Error(), fmt.Sprintf("failed to add node %d as voter", i))
	}

	time.Sleep(2 * time.Second)

	//shutdown leader
	err = nodes[0].Shutdown()
	require.NoError(t, err, "failed to shutdown leader node 0")

	//wait for new leader election
	time.Sleep(3 * time.Second)

	var newLeader *Node
	leaderCnt := 0

	for i := 1; i < 3; i++ {
		if nodes[i].IsLeader() {
			newLeader = nodes[i]
			leaderCnt++
		}
	}

	require.Equal(t, 1, leaderCnt, "there should be exactly one new leader")
	require.NotNil(t, newLeader, "new leader node should not be nil")

	//verify cluster is functional
	createResult, err := newLeader.Apply(types.CreateLeaseCmd{
		OwnerID: "client-2",
		TTL:     10 * time.Second,
	})
	require.NoError(t, err, "failed to create lease via new leader")

	createResp, ok := createResult.(fsm.CreateLeaseResponse)
	require.True(t, ok, "expected CreateLeaseResponse from new leader")
	assert.NotZero(t, createResp.LeaseID, "lease ID should not be zero from new leader")
}
