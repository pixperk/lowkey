package time

import "time"

// clock provides monotonic time since server start
// we will use time.Since which uses monotonic clock under the hood
// time.Now is not monotonic and can go backwards if system time is changed
// we will always move forward in monotonic time relative to a fixed start time
type Clock struct {
	startTime time.Time
}

func NewClock() *Clock {
	return &Clock{
		startTime: time.Now(),
	}
}

// duration since server start
// this duration is monotonic and always moves forward
func (c *Clock) Elapsed() time.Duration {
	return time.Since(c.startTime)
}

// returns the expiration time given a TTL
// expiration time is monotonic time since server start
func (c *Clock) ExpiresAt(ttl time.Duration) time.Duration {
	return c.Elapsed() + ttl
}
