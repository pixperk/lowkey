package types

// lock is a distributed mutex associated with a lease and has fencing token
// fencing token is critical and prevents split-brain scenarios
// fencing token is strictly monotonic and incremented on each successful lock acquisition
type Lock struct {
	Name         string //lock identifier
	OwnerID      string
	FencingToken uint64
	LeaseID      uint64 //associated lease
}
