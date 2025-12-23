package types

import "errors"

var (
	// Lease errors
	ErrLeaseNotFound   = errors.New("lease not found")
	ErrLeaseExpired    = errors.New("lease has expired")
	ErrInvalidLeaseTTL = errors.New("invalid lease TTL")

	// Lock errors
	ErrLockNotFound    = errors.New("lock not found")
	ErrLockAlreadyHeld = errors.New("lock is already held by another client")
	ErrNotLockOwner    = errors.New("caller is not the lock owner")
	ErrInvalidLeaseID  = errors.New("invalid lease ID")

	// Fencing errors
	ErrStaleToken = errors.New("fencing token is stale")
)
