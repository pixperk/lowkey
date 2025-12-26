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

// Phase 3: Lease System Tests

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
