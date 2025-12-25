package raft

import (
	"fmt"
	"net"
	"os"
	"time"

	"github.com/google/uuid"
	"github.com/hashicorp/raft"
	"github.com/pixperk/lowkey/pkg/fsm"
	"github.com/pixperk/lowkey/pkg/storage"
	"github.com/pixperk/lowkey/pkg/types"
	"google.golang.org/protobuf/proto"
)

// wraps a raft inst with out fsm and provides a clean api
type Node struct {
	raft    *raft.Raft
	fsm     *fsm.FSM
	raftFSM *fsm.RaftFSM
	cfg     *Config
}

type Config struct {
	NodeID    uuid.UUID //unique ID for this node
	BindAddr  string    //net addr to bind Raft communication
	DataDir   string    //data directory for Raft storage
	Bootstrap bool      //if this is the first node in the cluster
}

func NewNode(cfg *Config) (*Node, error) {
	if err := os.MkdirAll(cfg.DataDir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create data dir: %w", err)
	}

	raftFSM := fsm.NewRaftFSM()
	stateMachine := raftFSM.GetFSM()

	raftCfg := raft.DefaultConfig()
	raftCfg.LocalID = raft.ServerID(cfg.NodeID.String())

	raftCfg.HeartbeatTimeout = 1000 * time.Millisecond
	raftCfg.ElectionTimeout = 1000 * time.Millisecond
	raftCfg.CommitTimeout = 50 * time.Millisecond //time to wait before committing entries
	raftCfg.SnapshotThreshold = 8192              // snapshot after 8K log entries

	//add boltDB storage
	raftStorage, err := storage.NewBoltDBStorage(cfg.DataDir)
	if err != nil {
		return nil, fmt.Errorf("failed to create stores: %w", err)
	}

	//tcp transport for inter-node communication
	addr, err := net.ResolveTCPAddr("tcp", cfg.BindAddr)
	if err != nil {
		return nil, fmt.Errorf("failed to resolve bind addr: %w", err)
	}

	transport, err := raft.NewTCPTransport(cfg.BindAddr, addr, 3, 10*time.Second, os.Stderr)
	if err != nil {
		return nil, fmt.Errorf("failed to create transport: %w", err)
	}

	r, err := raft.NewRaft(raftCfg, raftFSM, raftStorage.LogStore, raftStorage.StableStore, raftStorage.SnapshotStore, transport)
	if err != nil {
		return nil, fmt.Errorf("failed to create raft: %w", err)
	}

	//bootstrap if needed
	if cfg.Bootstrap {
		configuration := raft.Configuration{
			Servers: []raft.Server{
				{
					ID:      raftCfg.LocalID,
					Address: transport.LocalAddr(),
				},
			},
		}

		r.BootstrapCluster(configuration)

	}

	return &Node{
		raft:    r,
		fsm:     stateMachine,
		raftFSM: raftFSM,
		cfg:     cfg,
	}, nil

}

// apply a command to the Raft cluster
func (n *Node) Apply(cmd types.Command) (any, error) {
	wrapper, err := cmd.ToProto()
	if err != nil {
		return nil, fmt.Errorf("failed to convert to proto: %w", err)
	}

	data, err := proto.Marshal(wrapper)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal proto: %w", err)
	}

	//replicate to cluster via Raft
	future := n.raft.Apply(data, 5*time.Second)
	if err := future.Error(); err != nil {
		return nil, fmt.Errorf("failed to apply command: %w", err)
	}

	return future.Response(), nil
}

// returns true if this node is the leader
func (n *Node) IsLeader() bool {
	return n.raft.State() == raft.Leader
}

// returns the leader's address
func (n *Node) GetLeader() string {
	leaderAddr, _ := n.raft.LeaderWithID()
	return string(leaderAddr)
}

// blocks until a leader is elected
func (n *Node) WaitForLeader(timeout time.Duration) error {
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	timeoutCh := time.After(timeout)

	for {
		select {
		case <-timeoutCh:
			return fmt.Errorf("no leader elected within timeout")
		case <-ticker.C:
			if n.GetLeader() != "" {
				return nil
			}
		}
	}
}

// returns FSM statistics
func (n *Node) Stats() fsm.Stats {
	return n.fsm.Stats()
}

// gracefully shuts down the Raft node
func (n *Node) Shutdown() error {
	return n.raft.Shutdown().Error()
}
