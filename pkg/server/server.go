package server

import (
	"context"
	"io"
	"time"

	pb "github.com/pixperk/lowkey/api/v1"
	"github.com/pixperk/lowkey/pkg/fsm"
	"github.com/pixperk/lowkey/pkg/raft"
	"github.com/pixperk/lowkey/pkg/types"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type Server struct {
	pb.UnimplementedLockServiceServer
	node *raft.Node
}

// wraps the raft node into a gRPC server
func NewServer(node *raft.Node) *Server {
	return &Server{
		node: node,
	}
}

func (s *Server) CreateLease(ctx context.Context, req *pb.CreateLeaseRequest) (*pb.CreateLeaseResponse, error) {
	if !s.node.IsLeader() {
		leaderAddr := s.node.GetLeader()
		return nil, notLeaderError(leaderAddr)
	}

	//validate request
	if req.OwnerId == "" {
		return nil, status.Error(codes.InvalidArgument, "owner_id required")
	}

	if req.TtlSeconds <= 0 {
		return nil, status.Error(codes.InvalidArgument, "ttl_seconds must be greater than 0")
	}

	//apply request via raft
	resp, err := s.node.Apply(types.CreateLeaseCmd{
		OwnerID: req.OwnerId,
		TTL:     time.Duration(req.TtlSeconds) * time.Second,
	})

	if err != nil {
		return nil, toGRPCError(err)
	}

	lease := resp.(fsm.CreateLeaseResponse)
	return &pb.CreateLeaseResponse{
		LeaseId:    lease.LeaseID,
		TtlSeconds: req.TtlSeconds,
	}, nil
}

func (s *Server) RenewLease(ctx context.Context, req *pb.RenewLeaseRequest) (*pb.RenewLeaseResponse, error) {
	if !s.node.IsLeader() {
		leaderAddr := s.node.GetLeader()
		return nil, notLeaderError(leaderAddr)
	}

	result, err := s.node.Apply(types.RenewLeaseCmd{
		LeaseID: req.LeaseId,
	})

	if err != nil {
		return nil, toGRPCError(err)
	}

	resp := result.(fsm.RenewLeaseResponse)
	ttlRemaining := resp.ExpiresAt
	return &pb.RenewLeaseResponse{
		TtlSeconds: int64(ttlRemaining.Seconds()),
	}, nil
}

func (s *Server) AcquireLock(ctx context.Context, req *pb.AcquireLockRequest) (*pb.AcquireLockResponse, error) {
	if !s.node.IsLeader() {
		leaderAddr := s.node.GetLeader()
		return nil, notLeaderError(leaderAddr)
	}

	if req.OwnerId == "" || req.LockName == "" {
		return nil, status.Error(codes.InvalidArgument, "owner_id, lock_name and lease_id are required")
	}

	result, err := s.node.Apply(types.AcquireLockCmd{
		LockName: req.LockName,
		OwnerID:  req.OwnerId,
		LeaseID:  req.LeaseId,
	})

	if err != nil {
		return nil, toGRPCError(err)
	}

	resp := result.(fsm.AcquireLockResponse)
	return &pb.AcquireLockResponse{
		FencingToken:    resp.FencingToken,
		LeaseTtlSeconds: int64(resp.LeaseTTL.Seconds()),
	}, nil
}

func (s *Server) ReleaseLock(ctx context.Context, req *pb.ReleaseLockRequest) (*pb.ReleaseLockResponse, error) {
	if !s.node.IsLeader() {
		leaderAddr := s.node.GetLeader()
		return nil, notLeaderError(leaderAddr)
	}

	if req.LockName == "" {
		return nil, status.Error(codes.InvalidArgument, "lock_name required")
	}

	result, err := s.node.Apply(types.ReleaseLockCmd{
		LockName: req.LockName,
		LeaseID:  req.LeaseId,
	})

	if err != nil {
		return nil, toGRPCError(err)
	}

	resp := result.(fsm.ReleaseLockResponse)
	return &pb.ReleaseLockResponse{
		Released: resp.Released,
	}, nil
}

func (s *Server) Heartbeat(stream pb.LockService_HeartbeatServer) error {
	for {
		//receive heartbeat from client
		req, err := stream.Recv()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}

		if !s.node.IsLeader() {
			return notLeaderError(s.node.GetLeader())
		}

		//renew the lease
		result, err := s.node.Apply(types.RenewLeaseCmd{
			LeaseID: req.LeaseId,
		})

		if err != nil {
			return toGRPCError(err)
		}

		resp := result.(fsm.RenewLeaseResponse)
		ttlRemaining := resp.ExpiresAt

		err = stream.Send(&pb.HeartbeatResponse{
			LeaseId:    req.LeaseId,
			TtlSeconds: int64(ttlRemaining.Seconds()),
		})

		if err != nil {
			return err
		}
	}
}

func (s *Server) GetStatus(ctx context.Context, req *pb.GetStatusRequest) (*pb.GetStatusResponse, error) {
	stats := s.node.Stats()

	return &pb.GetStatusResponse{
		NodeId:        s.node.GetNodeID().String(),
		IsLeader:      s.node.IsLeader(),
		LeaderAddress: s.node.GetLeader(),
		ClusterSize:   int32(s.node.GetClusterSize()),
		State:         s.node.GetState().String(),
		Stats: &pb.Stats{
			Leases:         int32(stats.Leases),
			Locks:          int32(stats.Locks),
			FencingCounter: stats.FencingCounter,
		},
	}, nil
}
