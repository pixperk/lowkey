package types

import (
	"time"
)

// Command interface for internal FSM operations
// These are the Go types we use internally (not protobuf)
type Command interface {
	Type() CommandType
	ToProto() (*CommandWrapper, error)
}

// Internal Go command types (these are what FSM uses)

// CreateLeaseCmd creates a new lease (internal type)
type CreateLeaseCmd struct {
	OwnerID string
	TTL     time.Duration
}

func (c CreateLeaseCmd) Type() CommandType { return CommandType_COMMAND_TYPE_CREATE_LEASE }

func (c CreateLeaseCmd) ToProto() (*CommandWrapper, error) {
	return &CommandWrapper{
		Type: CommandType_COMMAND_TYPE_CREATE_LEASE,
		Payload: &CommandWrapper_CreateLease{
			CreateLease: &CreateLeaseCommand{
				OwnerId:  c.OwnerID,
				TtlNanos: c.TTL.Nanoseconds(),
			},
		},
	}, nil
}

// RenewLeaseCmd renews an existing lease (internal type)
type RenewLeaseCmd struct {
	LeaseID uint64
}

func (c RenewLeaseCmd) Type() CommandType { return CommandType_COMMAND_TYPE_RENEW_LEASE }

func (c RenewLeaseCmd) ToProto() (*CommandWrapper, error) {
	return &CommandWrapper{
		Type: CommandType_COMMAND_TYPE_RENEW_LEASE,
		Payload: &CommandWrapper_RenewLease{
			RenewLease: &RenewLeaseCommand{
				LeaseId: c.LeaseID,
			},
		},
	}, nil
}

// AcquireLockCmd acquires a lock (internal type)
type AcquireLockCmd struct {
	LockName string
	OwnerID  string
	LeaseID  uint64
}

func (c AcquireLockCmd) Type() CommandType { return CommandType_COMMAND_TYPE_ACQUIRE_LOCK }

func (c AcquireLockCmd) ToProto() (*CommandWrapper, error) {
	return &CommandWrapper{
		Type: CommandType_COMMAND_TYPE_ACQUIRE_LOCK,
		Payload: &CommandWrapper_AcquireLock{
			AcquireLock: &AcquireLockCommand{
				LockName: c.LockName,
				OwnerId:  c.OwnerID,
				LeaseId:  c.LeaseID,
			},
		},
	}, nil
}

// ReleaseLockCmd releases a lock (internal type)
type ReleaseLockCmd struct {
	LockName string
	LeaseID  uint64
}

func (c ReleaseLockCmd) Type() CommandType { return CommandType_COMMAND_TYPE_RELEASE_LOCK }

func (c ReleaseLockCmd) ToProto() (*CommandWrapper, error) {
	return &CommandWrapper{
		Type: CommandType_COMMAND_TYPE_RELEASE_LOCK,
		Payload: &CommandWrapper_ReleaseLock{
			ReleaseLock: &ReleaseLockCommand{
				LockName: c.LockName,
				LeaseId:  c.LeaseID,
			},
		},
	}, nil
}

// ExpireLeaseCmd expires a lease (internal type)
type ExpireLeaseCmd struct {
	LeaseID uint64
}

func (c ExpireLeaseCmd) Type() CommandType { return CommandType_COMMAND_TYPE_EXPIRE_LEASE }

func (c ExpireLeaseCmd) ToProto() (*CommandWrapper, error) {
	return &CommandWrapper{
		Type: CommandType_COMMAND_TYPE_EXPIRE_LEASE,
		Payload: &CommandWrapper_ExpireLease{
			ExpireLease: &ExpireLeaseCommand{
				LeaseId: c.LeaseID,
			},
		},
	}, nil
}

// FromProtoCommand converts protobuf CommandWrapper to internal Command
func FromProtoCommand(wrapper *CommandWrapper) (Command, error) {
	switch wrapper.Type {
	case CommandType_COMMAND_TYPE_CREATE_LEASE:
		pb := wrapper.GetCreateLease()
		return CreateLeaseCmd{
			OwnerID: pb.OwnerId,
			TTL:     time.Duration(pb.TtlNanos),
		}, nil

	case CommandType_COMMAND_TYPE_RENEW_LEASE:
		pb := wrapper.GetRenewLease()
		return RenewLeaseCmd{
			LeaseID: pb.LeaseId,
		}, nil

	case CommandType_COMMAND_TYPE_ACQUIRE_LOCK:
		pb := wrapper.GetAcquireLock()
		return AcquireLockCmd{
			LockName: pb.LockName,
			OwnerID:  pb.OwnerId,
			LeaseID:  pb.LeaseId,
		}, nil

	case CommandType_COMMAND_TYPE_RELEASE_LOCK:
		pb := wrapper.GetReleaseLock()
		return ReleaseLockCmd{
			LockName: pb.LockName,
			LeaseID:  pb.LeaseId,
		}, nil

	case CommandType_COMMAND_TYPE_EXPIRE_LEASE:
		pb := wrapper.GetExpireLease()
		return ExpireLeaseCmd{
			LeaseID: pb.LeaseId,
		}, nil

	default:
		return nil, ErrUnknownCommand
	}
}
