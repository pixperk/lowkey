package fsm

import (
	"fmt"
	"testing"
	"time"

	"github.com/pixperk/lowkey/pkg/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestCreateLease tests lease creation
func TestCreateLease(t *testing.T) {
	fsm := NewFSM()

	cmd := types.CreateLeaseCmd{
		OwnerID: "client-1",
		TTL:     10 * time.Second,
	}

	result, err := fsm.Apply(cmd)
	require.NoError(t, err)

	resp, ok := result.(CreateLeaseResponse)
	require.True(t, ok, "expected CreateLeaseResponse")
	assert.NotZero(t, resp.LeaseID)

	// Verify lease was stored
	lease, exists := fsm.GetLease(resp.LeaseID)
	require.True(t, exists, "lease should exist")
	assert.Equal(t, "client-1", lease.OwnerID)
	assert.Equal(t, 10*time.Second, lease.TTL)
}

// TestRenewLease tests lease renewal
func TestRenewLease(t *testing.T) {
	fsm := NewFSM()

	// Create lease
	createResp, _ := fsm.Apply(types.CreateLeaseCmd{
		OwnerID: "client-1",
		TTL:     5 * time.Second,
	})
	leaseID := createResp.(CreateLeaseResponse).LeaseID

	// Renew lease
	result, err := fsm.Apply(types.RenewLeaseCmd{
		LeaseID: leaseID,
	})

	require.NoError(t, err)

	resp, ok := result.(RenewLeaseResponse)
	require.True(t, ok, "expected RenewLeaseResponse")
	assert.Greater(t, resp.ExpiresAt, time.Duration(0))
}

// TestAcquireLock tests lock acquisition with fencing token
func TestAcquireLock(t *testing.T) {
	fsm := NewFSM()

	// Create lease first
	createResp, _ := fsm.Apply(types.CreateLeaseCmd{
		OwnerID: "client-1",
		TTL:     10 * time.Second,
	})
	leaseID := createResp.(CreateLeaseResponse).LeaseID

	// Acquire lock
	result, err := fsm.Apply(types.AcquireLockCmd{
		LockName: "my-lock",
		OwnerID:  "client-1",
		LeaseID:  leaseID,
	})

	require.NoError(t, err)

	resp, ok := result.(AcquireLockResponse)
	require.True(t, ok, "expected AcquireLockResponse")
	assert.NotZero(t, resp.FencingToken)

	// Verify lock was stored
	lock, exists := fsm.GetLock("my-lock")
	require.True(t, exists, "lock should exist")
	assert.Equal(t, "client-1", lock.OwnerID)
	assert.Equal(t, resp.FencingToken, lock.FencingToken)
}

// TestFencingTokenMonotonicity tests that fencing tokens strictly increase
func TestFencingTokenMonotonicity(t *testing.T) {
	fsm := NewFSM()

	// Create lease
	createResp, _ := fsm.Apply(types.CreateLeaseCmd{
		OwnerID: "client-1",
		TTL:     10 * time.Second,
	})
	leaseID := createResp.(CreateLeaseResponse).LeaseID

	// Acquire multiple locks and verify tokens increase
	tokens := make([]uint64, 10)

	for i := 0; i < 10; i++ {
		lockName := fmt.Sprintf("lock-%d", i)

		result, err := fsm.Apply(types.AcquireLockCmd{
			LockName: lockName,
			OwnerID:  "client-1",
			LeaseID:  leaseID,
		})

		require.NoError(t, err)
		resp := result.(AcquireLockResponse)
		tokens[i] = resp.FencingToken
	}

	// Verify strictly increasing
	for i := 1; i < len(tokens); i++ {
		assert.Greater(t, tokens[i], tokens[i-1], "tokens must be strictly increasing")
	}

	// Final token should be 10
	assert.Equal(t, uint64(10), tokens[9])
}

// TestLockAlreadyHeld tests that a lock can't be acquired if held by another
func TestLockAlreadyHeld(t *testing.T) {
	fsm := NewFSM()

	// Create two leases
	lease1Resp, _ := fsm.Apply(types.CreateLeaseCmd{
		OwnerID: "client-1",
		TTL:     10 * time.Second,
	})
	lease1 := lease1Resp.(CreateLeaseResponse).LeaseID

	lease2Resp, _ := fsm.Apply(types.CreateLeaseCmd{
		OwnerID: "client-2",
		TTL:     10 * time.Second,
	})
	lease2 := lease2Resp.(CreateLeaseResponse).LeaseID

	// Client 1 acquires lock
	_, err := fsm.Apply(types.AcquireLockCmd{
		LockName: "my-lock",
		OwnerID:  "client-1",
		LeaseID:  lease1,
	})
	require.NoError(t, err, "client 1 should acquire")

	// Client 2 tries to acquire same lock (should fail)
	_, err = fsm.Apply(types.AcquireLockCmd{
		LockName: "my-lock",
		OwnerID:  "client-2",
		LeaseID:  lease2,
	})

	assert.ErrorIs(t, err, types.ErrLockAlreadyHeld)
}

// TestReleaseLock tests lock release
func TestReleaseLock(t *testing.T) {
	fsm := NewFSM()

	// Create lease and acquire lock
	createResp, _ := fsm.Apply(types.CreateLeaseCmd{
		OwnerID: "client-1",
		TTL:     10 * time.Second,
	})
	leaseID := createResp.(CreateLeaseResponse).LeaseID

	fsm.Apply(types.AcquireLockCmd{
		LockName: "my-lock",
		OwnerID:  "client-1",
		LeaseID:  leaseID,
	})

	// Release lock
	result, err := fsm.Apply(types.ReleaseLockCmd{
		LockName: "my-lock",
		LeaseID:  leaseID,
	})

	require.NoError(t, err)

	resp, ok := result.(ReleaseLockResponse)
	require.True(t, ok, "expected ReleaseLockResponse")
	assert.True(t, resp.Released)

	// Verify lock is gone
	_, exists := fsm.GetLock("my-lock")
	assert.False(t, exists, "lock should be deleted")
}

// TestExpireLeaseReleasesLocks tests that expiring a lease releases its locks
func TestExpireLeaseReleasesLocks(t *testing.T) {
	fsm := NewFSM()

	// Create lease
	createResp, _ := fsm.Apply(types.CreateLeaseCmd{
		OwnerID: "client-1",
		TTL:     10 * time.Second,
	})
	leaseID := createResp.(CreateLeaseResponse).LeaseID

	// Acquire 3 locks
	for i := 0; i < 3; i++ {
		lockName := fmt.Sprintf("lock-%d", i)
		fsm.Apply(types.AcquireLockCmd{
			LockName: lockName,
			OwnerID:  "client-1",
			LeaseID:  leaseID,
		})
	}

	// Verify 3 locks exist
	stats := fsm.Stats()
	assert.Equal(t, 3, stats.Locks)

	// Expire the lease
	result, err := fsm.Apply(types.ExpireLeaseCmd{
		LeaseID: leaseID,
	})

	require.NoError(t, err)

	resp := result.(ExpireLeaseResponse)
	assert.Equal(t, 3, resp.LocksReleased)

	// Verify all locks are gone
	stats = fsm.Stats()
	assert.Equal(t, 0, stats.Locks)

	// Verify lease is gone
	_, exists := fsm.GetLease(leaseID)
	assert.False(t, exists, "lease should be deleted")
}

// TestAcquireWithInvalidLease tests that you can't acquire with invalid lease
func TestAcquireWithInvalidLease(t *testing.T) {
	fsm := NewFSM()

	// Try to acquire with non-existent lease
	_, err := fsm.Apply(types.AcquireLockCmd{
		LockName: "my-lock",
		OwnerID:  "client-1",
		LeaseID:  999, // doesn't exist
	})

	assert.ErrorIs(t, err, types.ErrLeaseNotFound)
}
