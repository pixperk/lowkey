package metrics

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

var (
	// lock acquisition latency - histogram to track p50/p90/p99
	// tracks how long it takes to acquire a lock through raft consensus
	// labels: lock_name (to see which locks are slow)
	LockAcquireDuration = promauto.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "lowkey_lock_acquire_duration_seconds",
			Help:    "time taken to acquire a lock",
			Buckets: prometheus.ExponentialBuckets(0.001, 2, 10), // 1ms to 512ms
		},
		[]string{"lock_name"},
	)

	// lock acquisition counter - counts successes vs failures
	// use this to calculate success rate: success / (success + failure)
	// labels: lock_name, status (success/failure)
	LockAcquireTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "lowkey_lock_acquire_total",
			Help: "total number of lock acquisitions",
		},
		[]string{"lock_name", "status"},
	)

	// lock release counter - tracks clean releases
	// should roughly match successful acquisitions over time
	LockReleaseTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "lowkey_lock_release_total",
			Help: "total number of lock releases",
		},
		[]string{"lock_name"},
	)

	// currently held locks - gauge shows real-time active locks
	// useful for capacity planning and detecting lock leaks
	LocksActive = promauto.NewGauge(
		prometheus.GaugeOpts{
			Name: "lowkey_locks_active",
			Help: "current number of active locks",
		},
	)

	// lease creation counter - total leases created since startup
	// monotonically increasing, resets on restart
	LeaseCreateTotal = promauto.NewCounter(
		prometheus.CounterOpts{
			Name: "lowkey_lease_create_total",
			Help: "total number of leases created",
		},
	)

	// lease renewal counter - tracks heartbeat activity
	// high rate = healthy clients, low rate = possible issues
	LeaseRenewTotal = promauto.NewCounter(
		prometheus.CounterOpts{
			Name: "lowkey_lease_renew_total",
			Help: "total number of lease renewals (heartbeats)",
		},
	)

	// lease expiration counter - tracks client failures
	// spikes indicate network issues or crashed clients
	LeaseExpireTotal = promauto.NewCounter(
		prometheus.CounterOpts{
			Name: "lowkey_lease_expire_total",
			Help: "total number of lease expirations (client failures)",
		},
	)

	// active leases - gauge shows connected clients
	// should match number of healthy clients
	LeasesActive = promauto.NewGauge(
		prometheus.GaugeOpts{
			Name: "lowkey_leases_active",
			Help: "current number of active leases",
		},
	)

	// heartbeat counter - tracks keepalive success/failure
	// labels: status (success/failure)
	// use to detect heartbeat issues before lease expiration
	HeartbeatTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "lowkey_heartbeat_total",
			Help: "total number of heartbeats processed",
		},
		[]string{"status"},
	)

	// raft leader status - 1 if this node is leader, 0 if follower
	// exactly one node in cluster should have this = 1
	// use for alerting on leader elections
	RaftIsLeader = promauto.NewGauge(
		prometheus.GaugeOpts{
			Name: "lowkey_raft_is_leader",
			Help: "whether this node is the raft leader (1 = leader, 0 = follower)",
		},
	)

	// cluster size - number of peers in raft cluster
	// should be constant (e.g., 3 for 3-node cluster)
	// drop indicates node failure
	RaftPeers = promauto.NewGauge(
		prometheus.GaugeOpts{
			Name: "lowkey_raft_peers",
			Help: "number of peers in the raft cluster",
		},
	)

	// raft log index - last index applied to FSM
	// monotonically increasing, shows replication health
	// lag between leader and follower = replication delay
	RaftAppliedIndex = promauto.NewGauge(
		prometheus.GaugeOpts{
			Name: "lowkey_raft_applied_index",
			Help: "last raft log index applied to the fsm",
		},
	)

	// service uptime - always 1 when running
	// prometheus uses this to detect service restarts
	// scrape failure = 0 in prometheus (service down)
	Up = promauto.NewGauge(
		prometheus.GaugeOpts{
			Name: "lowkey_up",
			Help: "whether the service is up (always 1 when running)",
		},
	)
)

func init() {
	// set uptime gauge to 1 on startup
	Up.Set(1)
}
