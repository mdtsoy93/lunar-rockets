# Architecture decisions

## Service and storage

Use one HTTP service backed by a relational database. The service accepts rocket events and exposes the current state of individual rockets and a sortable list of rockets.

Kafka is not needed for the initial solution. It would preserve arrival order within a partition, but events can already arrive out of order, so application-level ordering would still be necessary. A database provides durable ingestion and atomic state updates with fewer operational dependencies.

Implement the service in Go with PostgreSQL for persistence. Use Docker Compose for the development environment and reproducible startup.

## Database migrations

Use `golang-migrate/migrate` to manage versioned SQL migrations. Keep paired `.up.sql` and `.down.sql` files in a `migrations/` directory and commit them alongside the application code.

Run migrations as a separate step before the HTTP service starts. In Docker Compose, wait for PostgreSQL to become healthy, run the migration service, and start the application only after migrations succeed. A migration failure must prevent application startup. Do not automatically run down migrations on shutdown.

## Event ordering

Treat `metadata.channel` as the rocket ID and `metadata.messageNumber` as the authoritative order within that channel. Interpret the README's ordering requirement as a contiguous sequence starting at 1. This is an explicit assumption of the solution.

Process events sequentially within each rocket. Different rockets may be processed concurrently. Message timestamps are informational and do not determine processing order.

## Event history and snapshots

Maintain two logical tables:

- `rockets`: the current state of each rocket, including `lastAppliedMessageNumber`.
- `rocket_events`: an immutable history of all accepted events, with a unique key on `(channel, messageNumber)`. Retain event metadata, payload, and server receipt time for future analytics and replay.

The snapshot represents exactly messages 1 through `lastAppliedMessageNumber`. Events in the history with a higher number are pending. Pending status is derived from the snapshot's sequence number; it does not require a separate table or a per-event applied flag.

For example, if the snapshot is at message 2 and message 4 arrives, persist message 4 without changing the snapshot. When message 3 arrives, apply messages 3 and 4 in order and advance the snapshot to 4.

Retain events after applying them. The history supports analytics and rebuilding snapshots after projection logic changes. A retry does not create another history row: this is a history of unique accepted events, not an audit of individual delivery attempts. Storage grows with event volume; retention, archival, and analytical query optimization can be added when requirements are known. No automatic deletion is planned initially.

## Idempotency and atomicity

The idempotency key is `(channel, messageNumber)`.

For each valid incoming event, serialize updates for its rocket and perform the following in one database transaction:

1. Insert the event into `rocket_events`, using the unique `(channel, messageNumber)` constraint to prevent duplicate rows.
2. If the key already exists, return success without comparing content or changing the stored event or snapshot. The producer is assumed to send identical content for a given key.
3. Apply the available contiguous sequence beginning at `lastAppliedMessageNumber + 1`.
4. Update the snapshot without deleting any events.
5. Commit before returning a successful HTTP response.

Serialize updates using PostgreSQL row locks. Create a placeholder rocket row with `INSERT ... ON CONFLICT DO NOTHING`, then acquire `SELECT ... FOR UPDATE` on that row before checking sequence progress and handling pending events. Perform these operations in the same transaction. This prevents simultaneous requests from applying the same event twice or overwriting a newer snapshot, including across service instances.

An event is immutable for its idempotency key. Idempotency uses only `(channel, messageNumber)`; payload conflict detection is outside the scope of this solution.

## Completeness

A snapshot is complete through its last applied sequence number. A pending event beyond the next expected number reveals a gap; the service must not skip that gap.

Having no known gaps does not prove that the state is final for a timestamp: another event may still be in transit. The protocol supplies no watermark or end-of-stream guarantee. Sequence progress and pending-message information can be exposed by the API without claiming time-based finality.

## Domain and service layers

`internal/domain` defines Rocket snapshots, immutable-by-convention Event values, typed event payloads, validation, and deterministic state transitions. It has no HTTP or database dependencies. Applying an event returns a new snapshot and leaves the original unchanged on error.

`internal/services` coordinates ingestion through an service-owned transactional storage interface. The PostgreSQL storage implementation in `internal/storage` acquires the rocket lock before invoking the callback and commits only when it succeeds. It uses read-committed isolation and rolls back on callback errors or panics. The service retains new events, ignores duplicates by key, and applies events until the next gap. The HTTP controller in `internal/controllers/http` converts the README's JSON envelope to typed domain events and delegates ingestion to this service; the executable wires storage, controllers, and services together.

The event table's composite primary key enforces idempotency and supports fetching the next event within a channel. Payloads are stored as JSONB using the typed domain payload's field names. Event time is stored both as a queryable PostgreSQL timestamp and an exact UTC RFC3339 string: PostgreSQL timestamps have microsecond precision, while the retained event history preserves nanoseconds. Snapshot message time uses the exact string representation as well. Receipt time is recorded separately and excluded from duplicate comparisons.

Launch must be message 1. Channels, timestamps, rocket types, missions, and explosion reasons must be present where applicable; sequence numbers must be positive and speed deltas nonnegative. Speed uses signed 64-bit integers with overflow checks. Decreases apply exactly as reported, including negative resulting speeds, since the protocol does not specify clamping.

Explosion status and its reason remain recorded while later speed and mission telemetry continues to apply in sequence. A second distinct explosion event is an invalid transition, consistent with the README's once-only explosion description. Redelivery with the original event key remains a successful no-op.

Validation occurs before opening a transaction. State-dependent transition failures roll back the transaction. An invalid event already buffered behind a gap can therefore block projection progress; no automatic skipping or repair is implemented.

## Read API

The query service depends on a read-only storage interface, separate from ingestion transactions. PostgreSQL reads each snapshot and its pending-event count in a single SQL statement to keep them consistent. Reads do not acquire ingestion locks or create placeholder rows.

`GET /rockets/{channel}` returns the current snapshot, or 404 for an unknown channel or a channel awaiting launch. `GET /rockets` includes only initialized rockets and supports sorting by channel, type, speed, or mission, ascending or descending. Channel ascending breaks ties. Default pagination uses limit 100 and offset 0, with a maximum limit of 1000. Invalid query parameters return 400. List responses contain rockets, limit, and offset; no separate total-count query is performed.

Both endpoints expose last applied message number/time, pending message count, and whether known gaps exist. Pending count is computed using the event table's composite primary key. Offset pagination and database sorting keep the initial design simple; independent pages can shift during concurrent updates and large offsets or frequent sorts may require optimization at scale. No additional sort indexes are introduced without measured need.

## Verification

Cover duplicate delivery, shuffled delivery, gap closure, simultaneous requests for one rocket, and recovery after restarting with pending events. Verify transaction failures cannot leave a partially advanced snapshot or lose an acknowledged event. Verify applied events remain in the history and replay produces the same snapshot.
