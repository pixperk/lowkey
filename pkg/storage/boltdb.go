package storage

import (
	"os"
	"path/filepath"

	"github.com/hashicorp/raft"
	raftboltdb "github.com/hashicorp/raft-boltdb/v2"
)

// BoltDBStorage wraps Raft's BoltDB storage components
// logstore : stores the Raft log entries
// stablestore : stores stable Raft metadata [stable = survives restarts]
// snapshotstore : stores snapshots of FSM state
type BoltDBStorage struct {
	LogStore      raft.LogStore
	StableStore   raft.StableStore
	SnapshotStore raft.SnapshotStore
}

func NewBoltDBStorage(dataDir string) (*BoltDBStorage, error) {
	if err := os.MkdirAll(dataDir, 0755); err != nil {
		return nil, err
	}

	dbPath := filepath.Join(dataDir, "raft.db")

	//boltDB is used for both log and stable storage
	boltDB, err := raftboltdb.New(raftboltdb.Options{
		Path: dbPath,
	})

	if err != nil {
		return nil, err
	}

	//snapshot store (file-based)
	snapshotDir := filepath.Join(dataDir, "snapshots")
	snapShotStore, err := raft.NewFileSnapshotStore(snapshotDir, 3, os.Stderr)
	if err != nil {
		boltDB.Close()
		return nil, err
	}

	return &BoltDBStorage{
		LogStore:      boltDB,
		StableStore:   boltDB,
		SnapshotStore: snapShotStore,
	}, nil
}

func (b *BoltDBStorage) Close() error {
	if closer, ok := b.LogStore.(interface{ Close() error }); ok {
		return closer.Close()
	}
	return nil
}
