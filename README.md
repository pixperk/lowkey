# lowkey

<div align="center">

**Distributed locking with Raft consensus and fencing tokens**

*Strong consistency for distributed mutual exclusion*

[Quick Start](#quick-start) · [Go SDK](#go-sdk) · [Examples](#production-examples) · [API](#api-reference)

</div>

---

## Overview

**lowkey** is a distributed lock service built on Raft consensus. It provides strongly consistent locks with fencing tokens to prevent split-brain scenarios and stale writes.

lowkey is layered: client SDK on top, gRPC + HTTP gateway in the middle, HashiCorp Raft underneath, and the lock/lease FSM at the bottom. Lease renewals take a fast path on the leader (no Raft round-trip); only lock acquire/release go through consensus.

<p align="center">
  <img src="https://raw.githubusercontent.com/pixperk/lowkey/main/assets/architecture.png" alt="lowkey architecture: 4 layers - Client SDK, Server (gRPC + HTTP), Raft Consensus, FSM state" width="700">
</p>

**Use cases:**
- Distributed cron jobs (only one instance executes)
- Database migrations (ensure single execution)
- Leader election
- Critical section protection across multiple processes

**Core guarantees:**
- **Strong consistency**, CP in CAP, no split-brain under network partitions
- **Fencing tokens**, monotonically increasing counters that prevent stale writes
- **Automatic cleanup**, lease-based locks release automatically on client failure

---

## Why lowkey?

**The problem with naive locking.** Multiple service instances need to coordinate. You add a distributed lock. But:
- Networks partition, so multiple "leaders" appear
- Processes pause (GC, CPU), so stale lock holders exist
- Clients crash, so locks are held forever

**How lowkey solves it.**
1. **Raft consensus**: only the majority partition can acquire locks
2. **Fencing tokens**: resources reject operations from stale lock holders
3. **Leases**: locks auto-release when clients stop heartbeating

**Comparison with alternatives:**

| System | Consensus | Fencing Tokens | Split-brain Protection |
|--------|-----------|----------------|------------------------|
| **lowkey** | Raft | yes | yes |
| etcd | Raft | yes | yes |
| Consul | Raft | no | yes |
| Redis Redlock | none | no | no |

### Consistency under partition

lowkey is CP: when the cluster splits, only the majority side keeps serving writes. The minority side rejects acquire/release until it rejoins. No two clients can ever hold the same lock with the same fencing token.

<p align="center">
  <img src="https://raw.githubusercontent.com/pixperk/lowkey/main/assets/cp.png" alt="CP under partition: minority side stops serving writes; majority retains the lock authority" width="600">
</p>

---

## Quick Start

### Installation

```bash
git clone https://github.com/pixperk/lowkey.git
cd lowkey
make build              # produces ./bin/lowkey
# or: go install ./cmd/lowkey
```

### Single Node (Development)

```bash
./bin/lowkey --bootstrap --data-dir ./data

# Server running on:
# - gRPC: :9000
# - HTTP: :8080
```

### HTTP API Example

```bash
# Create lease (60 second TTL)
curl -X POST http://localhost:8080/v1/lease \
  -d '{"owner_id":"client-1","ttl_seconds":60}'
# Response: {"lease_id":1}

# Acquire lock
curl -X POST http://localhost:8080/v1/lock/acquire \
  -d '{"lock_name":"my-job","owner_id":"client-1","lease_id":1}'
# Response: {"fencing_token":1}

# Release lock
curl -X POST http://localhost:8080/v1/lock/release \
  -d '{"lock_name":"my-job","lease_id":1}'
```

---

## Go SDK

### Installation

```bash
go get github.com/pixperk/lowkey/pkg/client
```

### Basic Usage

```go
package main

import (
    "context"
    "log"
    "time"

    "github.com/pixperk/lowkey/pkg/client"
)

func main() {
    c, err := client.NewClient("localhost:9000", "worker-1")
    if err != nil {
        log.Fatal(err)
    }
    defer c.Stop()

    // Create lease with automatic renewal
    ctx := context.Background()
    if err := c.Start(ctx, 10*time.Second); err != nil {
        log.Fatal(err)
    }

    // Acquire lock (returns fencing token)
    lock, err := c.Acquire(ctx, "my-job")
    if err != nil {
        log.Printf("Lock held by another instance: %v", err)
        return
    }
    defer lock.Release(ctx)

    token := lock.Token()
    log.Printf("Acquired lock with fencing token %d", token)

    // Use token in protected operations
    database.ExecuteWithToken(ctx, token, func() {
        // Critical section - only one instance executes this
    })
}
```

### SDK Reference

**Client API:**

| Method | Description |
|--------|-------------|
| `NewClient(addr, ownerID)` | Create client connection to lowkey server |
| `Start(ctx, ttl)` | Create lease with automatic heartbeat (renews every TTL/3) |
| `Acquire(ctx, lockName)` | Acquire lock, returns `*Lock` with fencing token |
| `Release(ctx, lockName)` | Release lock explicitly |
| `Status(ctx)` | Get cluster status and metrics |
| `Stop()` | Stop heartbeat goroutine and close connection |

**Lock API:**

| Method | Description |
|--------|-------------|
| `Token()` | Get fencing token for this lock |
| `Release(ctx)` | Release this lock |

**Key behaviors:**
- Automatic heartbeats every TTL/3 via gRPC streaming
- Thread-safe for concurrent use
- Locks auto-release when lease expires
- Uses bidirectional gRPC streaming (more efficient than HTTP polling)

---

## Production Examples

### Using Fencing Tokens with Redis

Protect Redis operations by storing the fencing token alongside your data:

```go
import (
    "context"
    "fmt"
    "github.com/pixperk/lowkey/pkg/client"
    "github.com/redis/go-redis/v9"
)

func processJob(ctx context.Context, lockClient *client.Client, redisClient *redis.Client) error {
    lock, err := lockClient.Acquire(ctx, "daily-report")
    if err != nil {
        return fmt.Errorf("lock held by another instance: %w", err)
    }
    defer lock.Release(ctx)

    token := lock.Token()

    // Check if we have a stale token
    storedToken, _ := redisClient.Get(ctx, "daily-report:token").Uint64()
    if token < storedToken {
        return fmt.Errorf("stale token %d < %d, aborting", token, storedToken)
    }

    // Perform protected operation with token validation
    pipe := redisClient.TxPipeline()
    pipe.Set(ctx, "daily-report:token", token, 0)
    pipe.Set(ctx, "daily-report:data", "report-content", 0)

    _, err = pipe.Exec(ctx)
    return err
}
```

**Key insight:** Even if a paused client wakes up with an expired lock, Redis will reject the write because `token < storedToken`.

### Using Fencing Tokens with Postgres

Store the fencing token in a dedicated column and use conditional updates:

```sql
CREATE TABLE jobs (
    name TEXT PRIMARY KEY,
    last_run TIMESTAMP,
    last_token BIGINT NOT NULL DEFAULT 0
);
```

```go
import (
    "context"
    "database/sql"
    "github.com/pixperk/lowkey/pkg/client"
)

func runDatabaseJob(ctx context.Context, lockClient *client.Client, db *sql.DB) error {
    lock, err := lockClient.Acquire(ctx, "db-migration")
    if err != nil {
        return err
    }
    defer lock.Release(ctx)

    token := lock.Token()

    // Conditional update: only succeed if our token is newer
    result, err := db.ExecContext(ctx, `
        UPDATE jobs
        SET last_run = NOW(), last_token = $1
        WHERE name = $2 AND last_token < $1
    `, token, "db-migration")

    if err != nil {
        return err
    }

    rows, _ := result.RowsAffected()
    if rows == 0 {
        return fmt.Errorf("stale token, another instance already ran")
    }

    // Safe to proceed - we have the freshest token
    return runMigration(ctx, db)
}
```

### Using Fencing Tokens with S3

Prevent split-brain writes to object storage:

```go
func uploadWithToken(ctx context.Context, lockClient *client.Client, s3Client *s3.Client) error {
    lock, err := lockClient.Acquire(ctx, "backup-upload")
    if err != nil {
        return err
    }
    defer lock.Release(ctx)

    token := lock.Token()

    // First, check the current token in metadata
    head, err := s3Client.HeadObject(ctx, &s3.HeadObjectInput{
        Bucket: aws.String("backups"),
        Key:    aws.String("latest.tar.gz"),
    })

    if err == nil {
        storedToken, _ := strconv.ParseUint(head.Metadata["Fencing-Token"], 10, 64)
        if token <= storedToken {
            return fmt.Errorf("stale token, aborting upload")
        }
    }

    // Upload with token in metadata
    _, err = s3Client.PutObject(ctx, &s3.PutObjectInput{
        Bucket: aws.String("backups"),
        Key:    aws.String("latest.tar.gz"),
        Body:   data,
        Metadata: map[string]string{
            "Fencing-Token": fmt.Sprintf("%d", token),
        },
    })

    return err
}
```

**Pattern:** Always include the fencing token with your write operations. The protected resource validates `new_token > stored_token` before accepting writes.

---

## Deployment

### Single Node (Development)

```bash
./bin/lowkey --bootstrap --data-dir ./data
```

### Multi-Node Cluster

The first node bootstraps; the rest join via the bootstrap node's **gRPC** address. `--join` accepts a comma-separated seed list. The joiner walks the list and skips any seed that responds *NotLeader*, so listing several known members makes the join robust to which node is currently leader.

**Node 1 (bootstrap):**
```bash
./bin/lowkey \
  --node-id <uuid-1> \
  --raft-addr 10.0.1.1:7000 \
  --grpc-addr 10.0.1.1:9000 \
  --http-addr :8080 \
  --data-dir /var/lib/lowkey \
  --bootstrap
```

**Node 2:**
```bash
./bin/lowkey \
  --node-id <uuid-2> \
  --raft-addr 10.0.1.2:7000 \
  --grpc-addr 10.0.1.2:9000 \
  --http-addr :8080 \
  --data-dir /var/lib/lowkey \
  --join 10.0.1.1:9000
```

**Node 3 (multiple seeds for robustness):**
```bash
./bin/lowkey \
  --node-id <uuid-3> \
  --raft-addr 10.0.1.3:7000 \
  --grpc-addr 10.0.1.3:9000 \
  --http-addr :8080 \
  --data-dir /var/lib/lowkey \
  --join 10.0.1.1:9000,10.0.1.2:9000
```

### Configuration Flags

| Flag | Default | Description |
|------|---------|-------------|
| `--node-id` | (generated UUID) | Unique node identifier |
| `--raft-addr` | `127.0.0.1:7000` | Raft consensus bind address |
| `--grpc-addr` | `:9000` | gRPC server listen address |
| `--http-addr` | `:8080` | HTTP gateway listen address |
| `--data-dir` | `./data` | Data directory for Raft logs and snapshots |
| `--bootstrap` | `false` | Bootstrap a new cluster (first node only) |
| `--join` | `""` | Comma-separated gRPC seed addresses to join an existing cluster |

`--bootstrap` and `--join` are mutually exclusive.

### Cluster Operations

**Check cluster status:**
```bash
curl http://localhost:8080/v1/status
```

```json
{
  "node_id": "9261ae0a-00cf-4463-9a55-445dba193fdf",
  "is_leader": true,
  "leader_address": "10.0.1.1:7000",
  "cluster_size": 3,
  "state": "leader",
  "stats": { "leases": 4, "locks": 2, "fencing_counter": 137 }
}
```

**Add a peer manually** (used by `--join` under the hood):
```bash
curl -X POST http://localhost:8080/v1/cluster/peers \
  -d '{"node_id":"<uuid>","raft_address":"10.0.1.4:7000"}'
```

**Remove a peer:**
```bash
curl -X DELETE http://localhost:8080/v1/cluster/peers/<uuid>
```

Membership RPCs are leader-only; non-leaders return `NotLeader` (gRPC `Unavailable`) with the current leader's address.

---

## How It Works

### Fencing Tokens Prevent Stale Writes

The token is a monotonically-increasing counter handed out at acquire time. The protected resource records the highest token it has seen and rejects any write with a lower one, so a stale lock holder coming back from a long pause cannot overwrite work done by the current holder.

<p align="center">
  <img src="https://raw.githubusercontent.com/pixperk/lowkey/main/assets/fencing.png" alt="Fencing tokens: Client A pauses, lease expires, Client B acquires with a higher token; A's late write is rejected because its token is lower" width="700">
</p>

**Key insight:** Even if a client holds a stale lock, the protected resource will reject its operations.

---

## API Reference

### HTTP REST API

| Endpoint | Method | Description |
|----------|--------|-------------|
| `/v1/lease` | POST | Create a new lease |
| `/v1/lease/renew` | POST | Renew an existing lease (polling) |
| `/v1/lock/acquire` | POST | Acquire a lock (returns fencing token) |
| `/v1/lock/release` | POST | Release a lock |
| `/v1/status` | GET | Get cluster status and statistics |
| `/v1/cluster/peers` | POST | Add a voting peer to the cluster (leader-only) |
| `/v1/cluster/peers/{node_id}` | DELETE | Remove a peer from the cluster (leader-only) |

### gRPC API

```protobuf
service LockService {
    rpc CreateLease(CreateLeaseRequest) returns (CreateLeaseResponse);
    rpc RenewLease(RenewLeaseRequest) returns (RenewLeaseResponse);
    rpc Heartbeat(stream HeartbeatRequest) returns (stream HeartbeatResponse);
    rpc AcquireLock(AcquireLockRequest) returns (AcquireLockResponse);
    rpc ReleaseLock(ReleaseLockRequest) returns (ReleaseLockResponse);
    rpc GetStatus(GetStatusRequest) returns (GetStatusResponse);
    rpc AddPeer(AddPeerRequest) returns (AddPeerResponse);
    rpc RemovePeer(RemovePeerRequest) returns (RemovePeerResponse);
}
```

**Note:** HTTP clients poll `/v1/lease/renew`, gRPC clients use the `Heartbeat` stream for efficiency.

---

## Performance

### Benchmarks

Using Go's built-in benchmark framework:

```bash
make bench-all                # throughput across scenarios
make bench-sequential         # single client baseline
make bench-parallel           # multiple clients, unique locks
make bench-contention         # multiple clients competing
make bench-percentiles        # p50, p90, p99, p99.9
```

**Throughput** (single-node lowkey on AMD Ryzen 7 5800HS):

| Benchmark | Operations | Latency/op | Scenario |
|-----------|-----------|------------|----------|
| Sequential | 4,460 | 3.24ms | Single client, measures Raft consensus + bbolt fsync overhead |
| Parallel | 19,911 | 0.60ms | Multiple clients, unique locks (true throughput) |
| Contention | 10,000 | 1.40ms | Multiple clients competing for same lock |

**Latency Percentiles** (1000 samples each):

| Scenario | p50 | p90 | p99 | p99.9 |
|----------|-----|-----|-----|-------|
| Sequential | 2.5ms | 2.7ms | 3.1ms | 6.5ms |
| Parallel | 4.4ms | 5.5ms | 7.0ms | 25ms |
| Contention | 5.5ms | 6.5ms | 7.6ms | 7.6ms |

> **Caveat:** These numbers are from a **single-node** lowkey, so the Raft `Apply` only touches local bbolt, no network replication. A 3-node cluster will pay an extra quorum round-trip per write. Treat the numbers as an upper bound, not a comparison against multi-node etcd/Consul deployments.

### Testing

```bash
make test                     # unit + raft integration tests
make test-coverage            # HTML coverage report
```

Percentile benches require a live lowkey at `localhost:9000`; they skip cleanly when nothing is listening (so `go test ./...` stays green in CI).

---

## Observability

### Prometheus Metrics

lowkey exposes Prometheus metrics at `/metrics` for monitoring lock performance, cluster health, and resource usage.

```bash
curl http://localhost:8080/metrics
```

### Quick Start with Docker Compose

```bash
make obs-up                   # starts Prometheus + Grafana
./bin/lowkey --bootstrap --data-dir ./data
```

- **Grafana**: http://localhost:3000 (admin / admin)
- **Prometheus**: http://localhost:9090

The lowkey dashboard is auto-provisioned from `grafana-provisioning/dashboards/lowkey-dashboard.json` (12 panels covering lock latency, cluster health, throughput, failures).

**Stop the stack:** `make obs-down` &nbsp;·&nbsp; **Logs:** `make obs-logs`

### Available Metrics

| Metric | Type | Labels | Description |
|--------|------|--------|-------------|
| `lowkey_lock_acquire_duration_seconds` | Histogram | `lock_name` | Time taken to acquire a lock (p50/p90/p99) |
| `lowkey_lock_acquire_total` | Counter | `lock_name`, `status` | Total lock acquisitions (success/failure) |
| `lowkey_lock_release_total` | Counter | `lock_name` | Total lock releases |
| `lowkey_locks_active` | Gauge | none | Currently held locks |
| `lowkey_lease_create_total` | Counter | none | Total leases created |
| `lowkey_lease_renew_total` | Counter | none | Total lease renewals (heartbeats) |
| `lowkey_lease_expire_total` | Counter | none | Total lease expirations (client failures) |
| `lowkey_leases_active` | Gauge | none | Currently active leases (connected clients) |
| `lowkey_heartbeat_total` | Counter | `status` | Heartbeat success/failure count |
| `lowkey_raft_is_leader` | Gauge | none | Whether this node is leader (1) or follower (0) |
| `lowkey_raft_peers` | Gauge | none | Number of peers in cluster |
| `lowkey_raft_applied_index` | Gauge | none | Last Raft log index applied to FSM |
| `lowkey_up` | Gauge | none | Service uptime (always 1 when running) |

**Example Prometheus queries:**

```promql
# lock acquisition success rate
sum(rate(lowkey_lock_acquire_total{status="success"}[5m]))
/
sum(rate(lowkey_lock_acquire_total[5m]))

# p99 lock latency
histogram_quantile(0.99,
  rate(lowkey_lock_acquire_duration_seconds_bucket[5m])
)

# active locks per lock name
sum by (lock_name) (lowkey_locks_active)

# lease expiration rate (client failures)
rate(lowkey_lease_expire_total[5m])

# cluster health: leader count (should always be 1)
sum(lowkey_raft_is_leader)
```

---

## Technical Details

**Raft Consensus Layer**
- HashiCorp Raft implementation
- Leader election and log replication
- Persistent storage with BoltDB
- Automatic snapshots

**Finite State Machine (FSM)**
- Lease management with monotonic time
- Lock acquisition with fencing tokens
- Automatic cleanup on lease expiration

**Dual API**
- gRPC with bidirectional streaming (efficient heartbeats)
- HTTP REST with JSON (easy integration)
- gRPC-Gateway for HTTP/gRPC translation

### Why this design?

- **Raft** is a proven algorithm with well-understood failure modes. CP in CAP, no split-brain under network partitions.
- **Fencing tokens** are monotonically-increasing counters: a mathematical guarantee against stale writes, provided the protected resource validates them.
- **Lease-based locks** clean themselves up when clients crash. No manual intervention needed, TTL is per client.

---

## Resources

**Papers and articles**
- [How to do distributed locking](https://martin.kleppmann.com/2016/02/08/how-to-do-distributed-locking.html) by Martin Kleppmann
- [The Chubby lock service for loosely-coupled distributed systems](https://research.google/pubs/pub27897/) by Google Research
- [In Search of an Understandable Consensus Algorithm](https://raft.github.io/raft.pdf), the Raft paper

**Implementations**
- [etcd](https://github.com/etcd-io/etcd), distributed KV store with locks
- [Consul](https://github.com/hashicorp/consul), service mesh with distributed locks
- [Chubby](https://research.google/pubs/pub27897/), Google's distributed lock service

---

## Built With

- [hashicorp/raft](https://github.com/hashicorp/raft), battle-tested Raft consensus implementation
- [grpc-ecosystem/grpc-gateway](https://github.com/grpc-ecosystem/grpc-gateway), HTTP/gRPC bidirectional translation
- [bbolt](https://github.com/etcd-io/bbolt), embedded key-value database for persistent storage
- [protobuf](https://protobuf.dev/), Protocol Buffers for efficient serialization

---

## License

MIT License, see [LICENSE](LICENSE) for details.

---

## Contributing

Contributions welcome. Open an issue or submit a pull request.

**Areas for contribution:**
- Client libraries for other languages (Python, Rust, Java)
- Observability (additional metrics, structured logging)
- Integration tests and chaos engineering
- Documentation and examples
