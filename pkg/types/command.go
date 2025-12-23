package types

import "time"

// type of FSM command
type CommandType uint

const (
	CommandTypeCreateLease CommandType = iota + 1
	CommandTypeRenewLease
	CommandTypeAcquireLock
	CommandTypeReleaseLock
	CommandTypeExpireLease
)

// interface all FSM commands implement
type Command interface {
	Type() CommandType
}

// creates a new lease
type CreateLeaseCommand struct {
	OwnerID string
	TTL     time.Duration
}

func (c CreateLeaseCommand) Type() CommandType { return CommandTypeCreateLease }

// renews an existing lease
type RenewLeaseCommand struct {
	LeaseID uint64
}

func (c RenewLeaseCommand) Type() CommandType { return CommandTypeRenewLease }

// acquires a lock with fencing
type AcquireLockCommand struct {
	LockName string
	OwnerID  string
	LeaseID  uint64
}

func (c AcquireLockCommand) Type() CommandType { return CommandTypeAcquireLock }

// releases a lock
type ReleaseLockCommand struct {
	LockName string
	LeaseID  uint64
}

func (c ReleaseLockCommand) Type() CommandType { return CommandTypeReleaseLock }

// expires a lease and releases all its locks (internal)
type ExpireLeaseCommand struct {
	LeaseID uint64
}

func (c ExpireLeaseCommand) Type() CommandType { return CommandTypeExpireLease }
