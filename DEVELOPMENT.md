# Development

The service uses Go, PostgreSQL, and golang-migrate. HTTP ingestion and rocket read endpoints are wired through application services to PostgreSQL.

## Docker Compose

Run `make up` to build the application, start PostgreSQL, apply migrations, and start the HTTP service on port 8088 in the background, then clear all rocket events and snapshots. Each invocation permanently deletes existing rocket data while preserving the schema and migration history. Stop any message generator before running it. Docker Desktop must be running. Migration failure prevents application startup and cleanup.

To start without clearing data, use `docker compose up -d --build`. View service logs with `docker compose logs -f app`.

Check `curl -f http://localhost:8088/health/live` and `curl -f http://localhost:8088/health/ready`. Readiness checks the database connection; liveness checks the HTTP server.

Run `docker compose down` to stop the environment while preserving database data. `docker compose down -v` also deletes the development database.

The credentials in Compose are for local development only.

## Cleaning development data

Stop any running message generator, then run:

```sh
make db-clean
```

This permanently removes all rocket events and snapshots from the Compose database. PostgreSQL must be running and migrations must already be applied. The two tables are cleared atomically; the schema and migration history remain intact. The service can keep running, and new messages can be ingested immediately. If a generator continues sending messages, data will appear again after cleanup.

For a complete database reset, use `docker compose down -v`, then `docker compose up -d --build`. This deletes the database volume and recreates the schema through migrations.

## Event ingestion

Post the README's JSON envelope to `POST /messages`:

```sh
curl -i http://localhost:8088/messages \
  -H 'Content-Type: application/json' \
  -d '{"metadata":{"channel":"193270a9-c9cf-404a-8f83-838e71d9ae67","messageNumber":1,"messageTime":"2022-02-02T19:39:05.86337+01:00","messageType":"RocketLaunched"},"message":{"type":"Falcon-9","launchSpeed":500,"mission":"ARTEMIS"}}'
```

The endpoint accepts all five README event types. It returns 200 after committing a new event or recognizing a duplicate key, 400 for malformed or invalid events, 413 for requests over 64 KiB, 422 for invalid state transitions, and 503 for storage failures or ingestion timeouts. A 200 may mean an event was durably buffered behind a gap; it does not promise that the snapshot has reached that event. Unknown JSON fields are ignored for forward compatibility. Numeric event fields must be explicitly present, including when zero.

Ingestion has a 10-second deadline. Internal failure details are logged rather than returned to clients.

Run the supplied generator with `./darwin_arm64/rockets launch http://localhost:8088/messages --message-delay=500ms --concurrency-level=1` (choose the executable for your platform).

## Reading rockets

```sh
curl -f http://localhost:8088/rockets/193270a9-c9cf-404a-8f83-838e71d9ae67
curl -f 'http://localhost:8088/rockets?sort=speed&order=desc&limit=20&offset=0'
```

`GET /rockets/{channel}` returns a rocket's current state, or 404 when the channel is unknown or its launch event has not yet been applied. Reads never create rocket rows.

```json
{
  "channel": "193270a9-c9cf-404a-8f83-838e71d9ae67",
  "type": "Falcon-9",
  "speed": 500,
  "mission": "ARTEMIS",
  "exploded": false,
  "lastAppliedMessageNumber": 1,
  "lastAppliedMessageTime": "2022-02-02T18:39:05.86337Z",
  "pendingMessageCount": 0,
  "hasKnownGaps": false
}
```

`explosionReason` is included when present. `pendingMessageCount` counts retained events above the applied sequence. `hasKnownGaps` is true when that count is positive; false does not guarantee that no events are still in transit.

`GET /rockets` returns `{"rockets": [...], "limit": 100, "offset": 0}`. An empty result uses `[]`. Only rockets with an applied launch are included. Supported query parameters:

| Parameter | Default | Accepted values |
| --- | --- | --- |
| `sort` | `channel` | `channel`, `type`, `speed`, `mission` |
| `order` | `asc` | `asc`, `desc` |
| `limit` | `100` | 1 through 1000 |
| `offset` | `0` | Nonnegative integer |

Ties are ordered by channel ascending. Invalid, unknown, or repeated query parameters return 400. Database failures and timeouts return 503; queries have a five-second deadline. Offset pages are independent reads, so concurrent updates can move rockets between pages.

## Running Go locally

Start dependencies and apply migrations:

```sh
docker compose up -d postgres
docker compose run --rm migrate
DATABASE_URL='postgres://rockets:rockets@localhost:5432/rockets?sslmode=disable' go run ./cmd/rockets
```

`DATABASE_URL` is required. `HTTP_ADDR` defaults to `:8088`. The service uses structured JSON logs and drains HTTP requests on SIGINT/SIGTERM, with a 10-second shutdown timeout.

## Migrations

Add sequentially numbered SQL pairs under `migrations/`. The initial baseline initializes migration tracking; migration 000002 creates rocket snapshots and retained event history. Migrations run separately from the application, using the pinned golang-migrate CLI image.

## Verification

```sh
go test ./...
go vet ./...
go build ./cmd/rockets
```

The default suite checks HTTP payload decoding, error responses, and health behavior. Core ingestion and read behavior is verified against real PostgreSQL, without a simulated transaction implementation. These integration tests are skipped unless `TEST_DATABASE_URL` is set:

```sh
TEST_DATABASE_URL='postgres://rockets:rockets@localhost:5432/rockets?sslmode=disable' go test -race ./...
```

The database user must be able to create schemas. Tests create and remove an isolated schema without changing application tables. They execute migration SQL up/down directly and cover shuffled delivery, persistence across separate connection pools, timestamp precision, duplicate delivery, concurrent ingestion, and transaction rollback. Compose's golang-migrate startup remains a separate environment check.
