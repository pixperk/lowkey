package types

import "time"

// a lease represents a time-bound ownership of a resource
// we cannot use the lock indefinitely
// after the lease expires, the resource can be claimed by others
type Lease struct {
	LeaseID   uint64
	OwnerID   string
	ExpiresAt time.Duration //monotonic time from server start
	TTL       time.Duration
}

// checks if the lease has expired given the elapsed time since server start
// elapsed is monotonic time from server start
func (l *Lease) IsExpired(elapsed time.Duration) bool {
	return elapsed >= l.ExpiresAt
}
