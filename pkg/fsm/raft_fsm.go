package fsm

import (
	"encoding/json"
	"io"

	"github.com/hashicorp/raft"
	"github.com/pixperk/lowkey/pkg/types"
	"google.golang.org/protobuf/proto"
)

// adapter to bridge Raft FSM with our internal FSM
type RaftFSM struct {
	fsm *FSM
}

func NewRaftFSM() *RaftFSM {
	return &RaftFSM{
		fsm: NewFSM(),
	}
}

func (rf *RaftFSM) Apply(log *raft.Log) any {
	//s1 : desrerialize proto from bytes
	var wrapper types.CommandWrapper
	if err := proto.Unmarshal(log.Data, &wrapper); err != nil {
		return err
	}

	//s2 : convert proto to internal command
	cmd, err := types.FromProtoCommand(&wrapper)
	if err != nil {
		return err
	}

	//s3 : apply internal command to FSM
	result, err := rf.fsm.Apply(cmd)
	if err != nil {
		return err
	}

	return result
}

// create a snapshot of the current FSM state
func (rf *RaftFSM) Snapshot() (raft.FSMSnapshot, error) {
	rf.fsm.mu.RLock()
	defer rf.fsm.mu.RUnlock()

	snapshot := &fsmSnapshot{
		Locks:          make(map[string]*types.Lock),
		Leases:         make(map[uint64]*types.Lease),
		FencingCounter: rf.fsm.fencingCounter,
		NextLeaseID:    rf.fsm.nextLeaseID,
	}

	//deep copy locks
	for name, lock := range rf.fsm.locks {
		lockCopy := *lock
		snapshot.Locks[name] = &lockCopy
	}

	//deep copy leases
	for id, lease := range rf.fsm.leases {
		leaseCopy := *lease
		snapshot.Leases[id] = &leaseCopy
	}

	return snapshot, nil
}

// restores FSM state from snapshot
// when a node falls behind and needs to catch up or a new node joins
func (rf *RaftFSM) Restore(snapshot io.ReadCloser) error {
	defer snapshot.Close()

	var snap fsmSnapshot
	if err := json.NewDecoder(snapshot).Decode(&snap); err != nil {
		return err
	}

	rf.fsm.mu.Lock()
	defer rf.fsm.mu.Unlock()

	rf.fsm.locks = snap.Locks
	rf.fsm.leases = snap.Leases
	rf.fsm.fencingCounter = snap.FencingCounter
	rf.fsm.nextLeaseID = snap.NextLeaseID

	return nil
}

// point-in-time snapshot of FSM state
type fsmSnapshot struct {
	Locks          map[string]*types.Lock  `json:"locks"`
	Leases         map[uint64]*types.Lease `json:"leases"`
	FencingCounter uint64                  `json:"fencing_counter"`
	NextLeaseID    uint64                  `json:"next_lease_id"`
}

// persist snapshot to given sink
func (s *fsmSnapshot) Persist(sink raft.SnapshotSink) error {
	if err := json.NewEncoder(sink).Encode(s); err != nil {
		sink.Cancel() //fail snapshot on error
		return err
	}
	return sink.Close() //mark snapshot as complete
}

// called when snapshot is no longer needed
// we have no resources to clean up here
func (s *fsmSnapshot) Release() {}
