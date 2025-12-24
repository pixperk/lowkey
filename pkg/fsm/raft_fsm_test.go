package fsm

import (
	"bytes"
	"io"
	"testing"
	"time"

	"github.com/hashicorp/raft"
	"github.com/pixperk/lowkey/pkg/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"
)

// TestRaftFSMApply tests that Apply works with protobuf serialization
func TestRaftFSMApply(t *testing.T) {
	raftFSM := NewRaftFSM()

	// Create a command
	cmd := types.CreateLeaseCmd{
		OwnerID: "client-1",
		TTL:     10 * time.Second,
	}

	// Convert to protobuf
	wrapper, err := cmd.ToProto()
	require.NoError(t, err)

	// Serialize to bytes (what Raft does)
	data, err := proto.Marshal(wrapper)
	require.NoError(t, err)

	// Create a Raft log entry
	logEntry := &raft.Log{
		Index: 1,
		Term:  1,
		Type:  raft.LogCommand,
		Data:  data,
	}

	// Apply through Raft FSM
	result := raftFSM.Apply(logEntry)

	// Verify result
	resp, ok := result.(CreateLeaseResponse)
	require.True(t, ok, "expected CreateLeaseResponse")
	assert.NotZero(t, resp.LeaseID)

	// Verify state was updated
	lease, exists := raftFSM.fsm.GetLease(resp.LeaseID)
	require.True(t, exists)
	assert.Equal(t, "client-1", lease.OwnerID)
}

// TestRaftFSMSnapshot tests snapshot creation
func TestRaftFSMSnapshot(t *testing.T) {
	raftFSM := NewRaftFSM()

	// Create some state
	raftFSM.fsm.Apply(types.CreateLeaseCmd{
		OwnerID: "client-1",
		TTL:     10 * time.Second,
	})

	raftFSM.fsm.Apply(types.CreateLeaseCmd{
		OwnerID: "client-2",
		TTL:     10 * time.Second,
	})

	// Create snapshot
	snapshot, err := raftFSM.Snapshot()
	require.NoError(t, err)

	// Verify snapshot contains state
	fsmSnap := snapshot.(*fsmSnapshot)
	assert.Equal(t, 2, len(fsmSnap.Leases))
	assert.Equal(t, uint64(3), fsmSnap.NextLeaseID) // Next would be 3
}

// TestRaftFSMRestore tests restoring from snapshot
func TestRaftFSMRestore(t *testing.T) {
	// Create original FSM with state
	original := NewRaftFSM()

	result1, _ := original.fsm.Apply(types.CreateLeaseCmd{
		OwnerID: "client-1",
		TTL:     10 * time.Second,
	})
	leaseID := result1.(CreateLeaseResponse).LeaseID

	original.fsm.Apply(types.AcquireLockCmd{
		LockName: "my-lock",
		OwnerID:  "client-1",
		LeaseID:  leaseID,
	})

	// Create snapshot
	snapshot, err := original.Snapshot()
	require.NoError(t, err)

	// Persist snapshot to buffer
	var buf bytes.Buffer
	mockSink := &mockSnapshotSink{buffer: &buf}
	err = snapshot.Persist(mockSink)
	require.NoError(t, err)

	// Create new FSM and restore from snapshot
	newFSM := NewRaftFSM()
	err = newFSM.Restore(io.NopCloser(&buf))
	require.NoError(t, err)

	// Verify new FSM has same state
	lease, exists := newFSM.fsm.GetLease(leaseID)
	require.True(t, exists)
	assert.Equal(t, "client-1", lease.OwnerID)

	lock, exists := newFSM.fsm.GetLock("my-lock")
	require.True(t, exists)
	assert.Equal(t, "client-1", lock.OwnerID)
}

// mockSnapshotSink implements raft.SnapshotSink for testing
type mockSnapshotSink struct {
	buffer *bytes.Buffer
}

func (m *mockSnapshotSink) Write(p []byte) (n int, err error) {
	return m.buffer.Write(p)
}

func (m *mockSnapshotSink) Close() error {
	return nil
}

func (m *mockSnapshotSink) ID() string {
	return "mock-snapshot"
}

func (m *mockSnapshotSink) Cancel() error {
	return nil
}
