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

func TestSnapshotAndRestore(t *testing.T) {
	tmpDir := t.TempDir()
	nodeID := uuid.New()

	cfg := &Config{
		NodeID:    nodeID,
		BindAddr:  "127.0.0.1:10000",
		DataDir:   tmpDir,
		Bootstrap: true,
	}

	node, err := NewNode(cfg)
	require.NoError(t, err, "failed to create node")

	err = node.WaitForLeader(5 * time.Second)
	require.NoError(t, err)

	var leaseID uint64
	for i := 0; i < 10; i++ {
		createResult, err := node.Apply(types.CreateLeaseCmd{
			OwnerID: fmt.Sprintf("client-%d", i),
			TTL:     10 * time.Second,
		})
		require.NoError(t, err)

		if i == 0 {
			createResp := createResult.(fsm.CreateLeaseResponse)
			leaseID = createResp.LeaseID
		}
	}

	// Acquire a lock
	acquireResult, err := node.Apply(types.AcquireLockCmd{
		LockName: "snapshot-lock",
		OwnerID:  "client-0",
		LeaseID:  leaseID,
	})
	require.NoError(t, err)
	acquireResp := acquireResult.(fsm.AcquireLockResponse)
	originalToken := acquireResp.FencingToken

	// Check stats before snapshot
	statsBefore := node.Stats()
	assert.Equal(t, 10, statsBefore.Leases)
	assert.Equal(t, 1, statsBefore.Locks)

	// Force a snapshot
	future := node.raft.Snapshot()
	require.NoError(t, future.Error(), "snapshot should succeed")

	// Shutdown node
	err = node.Shutdown()
	require.NoError(t, err)

	time.Sleep(500 * time.Millisecond)

	// Restart node (should restore from snapshot)
	cfg.Bootstrap = false
	node2, err := NewNode(cfg)
	require.NoError(t, err)
	defer node2.Shutdown()

	err = node2.WaitForLeader(5 * time.Second)
	require.NoError(t, err)

	// Verify state restored from snapshot
	statsAfter := node2.Stats()
	assert.Equal(t, statsBefore.Leases, statsAfter.Leases, "leases should be restored")
	assert.Equal(t, statsBefore.Locks, statsAfter.Locks, "locks should be restored")

	// Verify fencing counter persisted
	acquireResult2, err := node2.Apply(types.AcquireLockCmd{
		LockName: "another-lock",
		OwnerID:  "client-0",
		LeaseID:  leaseID,
	})
	require.NoError(t, err)

	acquireResp2 := acquireResult2.(fsm.AcquireLockResponse)
	assert.Greater(t, acquireResp2.FencingToken, originalToken, "fencing token should continue from snapshot")

}

func TestLeaseRenewal(t *testing.T) {
	tmpDir := t.TempDir()

	cfg := &Config{
		NodeID:    uuid.New(),
		BindAddr:  "127.0.0.1:11000",
		DataDir:   tmpDir,
		Bootstrap: true,
	}

	node, err := NewNode(cfg)
	require.NoError(t, err)
	defer node.Shutdown()

	err = node.WaitForLeader(5 * time.Second)
	require.NoError(t, err)

	//create lease with 5 second TTL
	createResult, err := node.Apply(types.CreateLeaseCmd{
		OwnerID: "client-1",
		TTL:     5 * time.Second,
	})
	require.NoError(t, err)

	createResp := createResult.(fsm.CreateLeaseResponse)
	leaseID := createResp.LeaseID
	originalExpiresAt := createResp.ExpiresAt

	//wait before renewal
	time.Sleep(1 * time.Second)

	//renew the lease
	renewResult, err := node.Apply(types.RenewLeaseCmd{
		LeaseID: leaseID,
	})
	require.NoError(t, err)

	renewResp := renewResult.(fsm.RenewLeaseResponse)
	newExpiresAt := renewResp.ExpiresAt

	//new expiry should be later than original
	assert.Greater(t, newExpiresAt, originalExpiresAt, "renewed lease should have later expiry time")

	//verify lease still exists
	stats := node.Stats()
	assert.Equal(t, 1, stats.Leases, "lease should still exist after renewal")
}

func TestLeaseExpiry(t *testing.T) {
	tmpDir := t.TempDir()

	cfg := &Config{
		NodeID:    uuid.New(),
		BindAddr:  "127.0.0.1:12000",
		DataDir:   tmpDir,
		Bootstrap: true,
	}

	node, err := NewNode(cfg)
	require.NoError(t, err)
	defer node.Shutdown()

	err = node.WaitForLeader(5 * time.Second)
	require.NoError(t, err)

	//create lease with short TTL
	createResult, err := node.Apply(types.CreateLeaseCmd{
		OwnerID: "client-1",
		TTL:     300 * time.Millisecond,
	})
	require.NoError(t, err)

	createResp := createResult.(fsm.CreateLeaseResponse)
	leaseID := createResp.LeaseID

	//acquire lock with the lease
	acquireResult, err := node.Apply(types.AcquireLockCmd{
		LockName: "expiry-test-lock",
		OwnerID:  "client-1",
		LeaseID:  leaseID,
	})
	require.NoError(t, err)
	assert.NotNil(t, acquireResult)

	//verify lease and lock exist
	statsBefore := node.Stats()
	assert.Equal(t, 1, statsBefore.Leases, "should have 1 lease")
	assert.Equal(t, 1, statsBefore.Locks, "should have 1 lock")

	//wait for lease to expire (TTL + expiry loop tick)
	time.Sleep(500 * time.Millisecond)

	//verify lease was deleted
	statsAfter := node.Stats()
	assert.Equal(t, 0, statsAfter.Leases, "lease should be expired and deleted")

	//verify lock was automatically released
	assert.Equal(t, 0, statsAfter.Locks, "lock should be released when lease expired")
}

func TestRenewExpiredLease(t *testing.T) {
	tmpDir := t.TempDir()

	cfg := &Config{
		NodeID:    uuid.New(),
		BindAddr:  "127.0.0.1:13000",
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

	//try to renew expired lease, should fail
	_, err = node.Apply(types.RenewLeaseCmd{
		LeaseID: leaseID,
	})
	require.Error(t, err, "renewing expired lease should fail")
	//could be ErrLeaseExpired or ErrLeaseNotFound (if expiry loop already deleted it)
	assert.True(t,
		err == types.ErrLeaseExpired || err == types.ErrLeaseNotFound,
		"should return ErrLeaseExpired or ErrLeaseNotFound, got: %v", err)
}

