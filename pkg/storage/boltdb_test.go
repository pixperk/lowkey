package storage

import (
	"os"
	"testing"

	"github.com/hashicorp/raft"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewBoltDBStores(t *testing.T) {
	// Create temp directory for test
	tmpDir, err := os.MkdirTemp("", "lowkey-test-*")
	require.NoError(t, err)
	defer os.RemoveAll(tmpDir)

	// Create stores
	stores, err := NewBoltDBStorage(tmpDir)
	require.NoError(t, err)
	defer stores.Close()

	// Verify stores are not nil
	assert.NotNil(t, stores.LogStore)
	assert.NotNil(t, stores.StableStore)
	assert.NotNil(t, stores.SnapshotStore)
}

func TestLogStore(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "lowkey-test-*")
	require.NoError(t, err)
	defer os.RemoveAll(tmpDir)

	stores, err := NewBoltDBStorage(tmpDir)
	require.NoError(t, err)
	defer stores.Close()

	// Store a log entry
	log := &raft.Log{
		Index: 1,
		Term:  1,
		Type:  raft.LogCommand,
		Data:  []byte("test data"),
	}

	err = stores.LogStore.StoreLog(log)
	require.NoError(t, err)

	// Retrieve the log entry
	retrievedLog := &raft.Log{}
	err = stores.LogStore.GetLog(1, retrievedLog)
	require.NoError(t, err)

	assert.Equal(t, uint64(1), retrievedLog.Index)
	assert.Equal(t, uint64(1), retrievedLog.Term)
	assert.Equal(t, []byte("test data"), retrievedLog.Data)
}

func TestStableStore(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "lowkey-test-*")
	require.NoError(t, err)
	defer os.RemoveAll(tmpDir)

	stores, err := NewBoltDBStorage(tmpDir)
	require.NoError(t, err)
	defer stores.Close()

	// Store current term
	err = stores.StableStore.SetUint64([]byte("currentTerm"), 5)
	require.NoError(t, err)

	// Retrieve current term
	term, err := stores.StableStore.GetUint64([]byte("currentTerm"))
	require.NoError(t, err)
	assert.Equal(t, uint64(5), term)
}

func TestSnapshotStore(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "lowkey-test-*")
	require.NoError(t, err)
	defer os.RemoveAll(tmpDir)

	stores, err := NewBoltDBStorage(tmpDir)
	require.NoError(t, err)
	defer stores.Close()

	// Create a snapshot
	snapshotData := []byte(`{"locks":{},"leases":{}}`)

	sink, err := stores.SnapshotStore.Create(
		raft.SnapshotVersionMax,
		100, // last included index
		1,   // last included term
		raft.Configuration{},
		1,   // configuration index
		nil, // transport
	)
	require.NoError(t, err)

	_, err = sink.Write(snapshotData)
	require.NoError(t, err)

	err = sink.Close()
	require.NoError(t, err)

	// List snapshots
	snapshots, err := stores.SnapshotStore.List()
	require.NoError(t, err)
	assert.Len(t, snapshots, 1)

	// Verify snapshot metadata
	assert.Equal(t, uint64(100), snapshots[0].Index)
	assert.Equal(t, uint64(1), snapshots[0].Term)
}

func TestStoresPersistence(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "lowkey-test-*")
	require.NoError(t, err)
	defer os.RemoveAll(tmpDir)

	// Create stores and write data
	stores1, err := NewBoltDBStorage(tmpDir)
	require.NoError(t, err)

	err = stores1.StableStore.SetUint64([]byte("currentTerm"), 42)
	require.NoError(t, err)

	stores1.Close()

	// Reopen stores and verify data persisted
	stores2, err := NewBoltDBStorage(tmpDir)
	require.NoError(t, err)
	defer stores2.Close()

	term, err := stores2.StableStore.GetUint64([]byte("currentTerm"))
	require.NoError(t, err)
	assert.Equal(t, uint64(42), term)
}
