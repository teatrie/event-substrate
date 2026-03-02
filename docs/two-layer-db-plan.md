# Two-Layer Database Architecture Plan

> **Status:** Discussion in progress. Pending decision on expected write volume.

## Motivation

Supabase (PostgreSQL) is best used as a **serving/presentation layer** — optimized for high read performance, RLS, Realtime subscriptions, and PostgREST. Internally, the platform needs a database that can handle **high write throughput** for stream processing output. In production cloud environments:

- **GCP:** Cloud Bigtable
- **AWS:** DynamoDB
- **Local dev:** ScyllaDB (CQL-compatible, Docker `scylladb/scylla`)

---

## Current State

Three Supabase tables receive writes from Flink and Go services:

| Table | Writers | Write Pattern | Read Pattern |
|---|---|---|---|
| `user_notifications` | 5 Flink SQL processors + Go message-consumer | High-volume append-only (every event funnels here) | Per-user via Realtime, 3-way OR in RLS (own events, broadcasts, DMs) |
| `credit_ledger` | Flink credit_balance_processor, PyFlink credit_check_processor | Append-only + atomic read-modify-write for balance check | `SUM(amount)` aggregation for balance, per-user list for display |
| `media_files` | Flink (INSERT on upload confirmed), Go media-service (UPDATE on delete) | Low volume INSERT + UPDATE | Point lookup by `(user_id, file_path)`, filtered listing by `status = 'active'` |

---

## Access Pattern Analysis

### 1. `user_notifications` — Highest Write Volume

The RLS policy queries across three dimensions:

```sql
user_id = auth.uid()                                           -- own events
OR (event_type = 'user.message' AND visibility = 'broadcast')  -- broadcasts
OR (event_type = 'user.message' AND recipient_id = auth.uid()) -- DMs to me
```

**Wide-column implication:** Requires three denormalized tables (one per access pattern):

```
notifications_by_user        — PK: (user_id), CK: (created_at DESC)
broadcast_notifications      — PK: (time_bucket), CK: (created_at DESC)
direct_messages_by_recipient — PK: (recipient_id), CK: (created_at DESC)
```

Every write becomes 3 writes (fan-out on ingest). Wide-column stores handle this well, but Postgres does it naturally with a single table + index.

### 2. `credit_ledger` — Trickiest Access Pattern

The atomic check-and-deduct is the critical path:

```sql
INSERT INTO credit_ledger (...)
SELECT ... WHERE (SELECT COALESCE(SUM(amount), 0) FROM credit_ledger WHERE user_id = %s) >= 1
```

Wide-column stores have no `SUM()`. Options:

| Approach | Mechanism | Tradeoff |
|---|---|---|
| **Materialized balance table** | Separate `credit_balances(user_id PK, balance INT)` + ScyllaDB LWT: `UPDATE ... SET balance = balance - 1 IF balance >= 1` | LWT is Paxos-based, ~10x slower than normal writes. Acceptable for per-upload QPS. |
| **Flink keyed state** | Keep balance in Flink's RocksDB state. Credit check is an in-memory lookup — zero DB roundtrip. Write ledger to ScyllaDB for audit only. | Best latency. Requires checkpointing to survive restarts. |
| **Counter column** | `balance COUNTER` in ScyllaDB | Counters don't support conditional writes (`IF`). Ruled out. |

**Recommendation:** Flink keyed state is the most natural fit for a streaming platform. The credit check is already a Flink job — lifting the balance into process-local state eliminates the Postgres roundtrip entirely. The ledger table becomes a pure audit log.

### 3. `media_files` — Simplest Mapping

Maps cleanly to wide-column:

```
media_files_by_user — PK: (user_id), CK: (file_path)
```

The `status = 'active'` filter is awkward in CQL (no efficient WHERE on non-key columns), but scanning a per-user partition and filtering client-side is fine at expected cardinality. The soft-delete pattern (`UPDATE status = 'deleted'`) maps to a CQL UPDATE on the partition key.

---

## Proposed ScyllaDB Schema (Model A)

```cql
-- Append-only notification log (write-optimized, no read concerns)
CREATE TABLE notifications (
    user_id TEXT,
    created_at TIMEUUID,
    event_type TEXT,
    payload TEXT,
    visibility TEXT,
    recipient_id TEXT,
    PRIMARY KEY ((user_id), created_at)
) WITH CLUSTERING ORDER BY (created_at DESC)
  AND default_time_to_live = 2592000;  -- 30-day TTL

-- Credit audit log (append-only, ledger entries)
CREATE TABLE credit_events (
    user_id TEXT,
    event_time TIMEUUID,
    amount INT,
    event_type TEXT,
    description TEXT,
    PRIMARY KEY ((user_id), event_time)
) WITH CLUSTERING ORDER BY (event_time DESC);

-- File metadata (mutable, supports soft-delete)
CREATE TABLE media_files (
    user_id TEXT,
    file_path TEXT,
    file_name TEXT,
    file_size BIGINT,
    media_type TEXT,
    status TEXT,
    upload_time TEXT,
    PRIMARY KEY ((user_id), file_path)
);
```

---

## Two Viable Models

### Model A: ScyllaDB as Durable Write Store

```
Flink → ScyllaDB (write-optimized, all tables)
ScyllaDB CDC → Kafka → Materializer consumer → Postgres (serving)
```

**Pros:**
- Clean CQRS separation
- ScyllaDB handles any write throughput
- TTLs auto-expire old notifications (no Postgres vacuum)
- Maps directly to Bigtable/DynamoDB in production

**Cons:**
- Extra hop (ScyllaDB → CDC → Kafka → Postgres)
- More infrastructure to operate
- Eventual consistency between write and read stores

**When to choose:** Write volume exceeds ~5-10K inserts/sec sustained on `user_notifications`, or multi-region deployment is required.

### Model B: Flink State + Postgres (with tuning)

```
Flink keyed state (credit balance, saga state) — in-process, zero latency
Flink JDBC sink → Postgres (with batching + connection pooling)
Postgres partitioning on user_notifications if write volume demands it
```

**Pros:**
- Simpler — fewer moving parts
- Postgres with PgBouncer + time-based partitioning handles more than expected
- No eventual consistency concerns
- Lower operational overhead

**Cons:**
- Postgres becomes the bottleneck ceiling
- At >10K writes/sec sustained, WAL and vacuum pressure becomes problematic

**When to choose:** Expected write volume is moderate, single-region deployment, want to minimize operational complexity.

---

## Recommendation

**Start with Model B.** The current architecture already has Kafka absorbing the write firehose and Flink providing stream processing. Postgres only sees the materialized output — a fraction of raw event volume.

**Regardless of model choice, adopt Flink keyed state for credit checks now.** This is a pure win — eliminates the Postgres roundtrip on the hottest path (`credit_check_processor.py`) without introducing new infrastructure.

**Migrate to Model A when:**
- `user_notifications` write volume exceeds partitioned Postgres capacity
- Multi-region deployment is needed (ScyllaDB's masterless replication)
- TTL-based auto-cleanup is preferred over Postgres vacuum

---

## Local Dev Stack (ScyllaDB)

When Model A is adopted, the local dev stack adds:

| Component | Image | Purpose |
|---|---|---|
| ScyllaDB | `scylladb/scylla` | Wide-column write store (Bigtable/DynamoDB analog) |
| Flink Cassandra connector | (JAR, already available) | Flink sink to ScyllaDB via CQL |

ScyllaDB runs well as a single-node Docker container (~512MB RAM). CQL is wire-compatible with Cassandra, so Flink's native Cassandra connector works out of the box.

---

## Open Questions

- [ ] Expected write volume at production scale — the hinge point for Model A vs B
- [ ] Whether to update `productionization.md` with this two-layer strategy
- [ ] Flink keyed state migration for `credit_check_processor.py` — standalone task or part of a larger epic
