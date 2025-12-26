package server

import (
	"github.com/pixperk/lowkey/pkg/types"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// converts domain errors tp gRPC status errors
func toGRPCError(err error) error {
	if err == nil {
		return nil
	}

	switch err {
	case types.ErrLeaseNotFound, types.ErrLockNotFound:
		return status.Error(codes.NotFound, err.Error())

	case types.ErrLeaseExpired, types.ErrLockAlreadyHeld, types.ErrStaleToken:
		return status.Error(codes.FailedPrecondition, err.Error())

	case types.ErrInvalidLeaseTTL:
		return status.Error(codes.InvalidArgument, err.Error())

	case types.ErrNotLockOwner:
		return status.Error(codes.PermissionDenied, err.Error())

	default:
		return status.Error(codes.Internal, err.Error())
	}
}

// returns a not leader error with the given leader address
// includes the current leader address in the error message
func notLeaderError(leaderAddr string) error {
	return status.Errorf(codes.Unavailable,
		"not leader, leader is at  : %s", leaderAddr)
}
