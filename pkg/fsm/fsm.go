package fsm

import (
	"fmt"
	"sync"

	tm "time"

	"github.com/pixperk/lowkey/pkg/time"
	"github.com/pixperk/lowkey/pkg/types"
)

// manages core lock and lease state
// critical :
// - fencing tokens must be strictly monotonic
// - locks must have valid leases
// - expired leases must release all associated locks
type FSM struct {
	mu sync.RWMutex

	locks  map[string]*types.Lock  // lock name -> Lock
	leases map[uint64]*types.Lease // lease ID -> Lease

	fencingCounter uint64 // global fencing token counter (monotonic)
	nextLeaseID    uint64 // next lease ID to assign

	clock *time.Clock // monotonic clock
}

func NewFSM() *FSM {
	return &FSM{
		locks:          make(map[string]*types.Lock),
		leases:         make(map[uint64]*types.Lease),
		fencingCounter: 0,
		nextLeaseID:    1, //start lease IDs from 1
		clock:          time.NewClock(),
	}
}

// applies a command to the FSM and returns the result or error
func (f *FSM) Apply(cmd types.Command) (any, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	switch c := cmd.(type) {
	case types.CreateLeaseCmd:
		return f.applyCreateLease(c)
	case types.RenewLeaseCmd:
		return f.applyRenewLease(c)
	case types.AcquireLockCmd:
		return f.applyAcquireLock(c)
	case types.ReleaseLockCmd:
		return f.applyReleaseLock(c)
	case types.ExpireLeaseCmd:
		return f.applyExpireLease(c)
	default:
		return nil, fmt.Errorf("unknown command type: %T", cmd)
	}
}

// returned when a lease is created
type CreateLeaseResponse struct {
	LeaseID   uint64
	ExpiresAt tm.Duration
}

func (f *FSM) applyCreateLease(cmd types.CreateLeaseCmd) (any, error) {
	if cmd.TTL <= 0 {
		return nil, types.ErrInvalidLeaseTTL
	}

	leaseID := f.nextLeaseID
	f.nextLeaseID++

	expiresAt := f.clock.ExpiresAt(cmd.TTL)

	lease := &types.Lease{
		LeaseID:   leaseID,
		OwnerID:   cmd.OwnerID,
		ExpiresAt: expiresAt,
		TTL:       cmd.TTL,
	}

	f.leases[leaseID] = lease

	return CreateLeaseResponse{
		LeaseID:   leaseID,
		ExpiresAt: expiresAt,
	}, nil
}

// returned when a lease is renewed
type RenewLeaseResponse struct {
	ExpiresAt tm.Duration
}

func (f *FSM) applyRenewLease(cmd types.RenewLeaseCmd) (any, error) {
	lease, exists := f.leases[cmd.LeaseID]
	if !exists {
		return nil, types.ErrLeaseNotFound
	}

	//if already expired, cannot renew
	if lease.IsExpired(f.clock.Elapsed()) {
		return nil, types.ErrLeaseExpired
	}

	lease.ExpiresAt = f.clock.ExpiresAt(lease.TTL)

	return RenewLeaseResponse{
		ExpiresAt: lease.ExpiresAt,
	}, nil
}

// returned when a lock is acquired
type AcquireLockResponse struct {
	FencingToken uint64
}

func (f *FSM) applyAcquireLock(cmd types.AcquireLockCmd) (any, error) {
	lease, exists := f.leases[cmd.LeaseID]
	if !exists {
		return nil, types.ErrLeaseNotFound
	}

	//if lease expired, cannot acquire lock
	if lease.IsExpired(f.clock.Elapsed()) {
		return nil, types.ErrLeaseExpired
	}

	//verify lease owner matches lock owner
	if lease.OwnerID != cmd.OwnerID {
		return nil, types.ErrNotLockOwner
	}

	if existingLock, held := f.locks[cmd.LockName]; held {
		//if held by same lease, allow re-acquisition (idempotent)
		if existingLock.LeaseID == cmd.LeaseID {
			return AcquireLockResponse{
				FencingToken: existingLock.FencingToken,
			}, nil
		}
		//held by different lease, cannot acquire
		return nil, types.ErrLockAlreadyHeld
	}

	//increment global fencing counter
	f.fencingCounter++
	fencingToken := f.fencingCounter

	lock := &types.Lock{
		Name:         cmd.LockName,
		OwnerID:      cmd.OwnerID,
		FencingToken: fencingToken,
		LeaseID:      cmd.LeaseID,
	}

	f.locks[cmd.LockName] = lock

	return AcquireLockResponse{
		FencingToken: fencingToken,
	}, nil

}

// returned when a lock is released
type ReleaseLockResponse struct {
	Released bool
}

func (f *FSM) applyReleaseLock(cmd types.ReleaseLockCmd) (any, error) {
	lock, held := f.locks[cmd.LockName]
	if !held {
		return nil, types.ErrLockNotFound
	}

	if lock.LeaseID != cmd.LeaseID {
		return nil, types.ErrNotLockOwner
	}

	delete(f.locks, cmd.LockName)

	return ReleaseLockResponse{
		Released: true,
	}, nil
}

// expires a lease and releases all its locks
type ExpireLeaseResponse struct {
	LocksReleased int
}

func (f *FSM) applyExpireLease(cmd types.ExpireLeaseCmd) (any, error) {
	lease, exists := f.leases[cmd.LeaseID]
	if !exists {
		return nil, types.ErrLeaseNotFound
	}

	//release all locks associated with this lease
	locksReleased := 0
	for lockName, lock := range f.locks {
		if lock.LeaseID == lease.LeaseID {
			delete(f.locks, lockName)
			locksReleased++
		}
	}

	//delete the lease
	delete(f.leases, lease.LeaseID)

	return ExpireLeaseResponse{
		LocksReleased: locksReleased,
	}, nil
}

// returns a lock by name
func (f *FSM) GetLock(lockName string) (*types.Lock, bool) {
	f.mu.RLock()
	defer f.mu.RUnlock()

	lock, exists := f.locks[lockName]
	return lock, exists
}

// returns a lease by ID
func (f *FSM) GetLease(leaseID uint64) (*types.Lease, bool) {
	f.mu.RLock()
	defer f.mu.RUnlock()

	lease, exists := f.leases[leaseID]
	return lease, exists
}

// current fsm stats
type Stats struct {
	Locks          int
	Leases         int
	FencingCounter uint64
}

func (f *FSM) Stats() Stats {
	f.mu.RLock()
	defer f.mu.RUnlock()

	return Stats{
		Locks:          len(f.locks),
		Leases:         len(f.leases),
		FencingCounter: f.fencingCounter,
	}
}

// returns all lease IDs that have expired
func (f *FSM) GetExpiredLeases(now tm.Duration) []uint64 {
	f.mu.RLock()
	defer f.mu.RUnlock()

	var expired []uint64
	for leaseID, lease := range f.leases {
		if lease.IsExpired(now) {
			expired = append(expired, leaseID)
		}
	}

	return expired
}

func (f *FSM) CurrentTime() tm.Duration {
	return f.clock.Elapsed()
}
