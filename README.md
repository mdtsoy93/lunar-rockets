# Rocket service

Go HTTP service for receiving rocket events and querying their current state. PostgreSQL retains the event history and snapshots. Events are applied in message-number order within each channel; duplicate keys are ignored and out-of-order events wait for missing messages.

## Quick start

Requirements: Docker with Compose (Docker Desktop on macOS), Make, and curl. Local Go installation is only needed for developing outside Docker. Ports `8088` and `5432` must be available.

From the project root, with Docker running:

```sh
make up
curl --fail http://localhost:8088/health/ready
```

`make up` builds and starts the stack in the background, applies migrations, and **deletes all existing rocket events and snapshots**. Stop message generators before running it.

To start while keeping existing data:

```sh
docker compose up -d --build
```

## Try the API

Send a launch event:

```sh
curl --fail-with-body -i http://localhost:8088/messages \
  -H 'Content-Type: application/json' \
  -d '{"metadata":{"channel":"193270a9-c9cf-404a-8f83-838e71d9ae67","messageNumber":1,"messageTime":"2022-02-02T19:39:05.86337+01:00","messageType":"RocketLaunched"},"message":{"type":"Falcon-9","launchSpeed":500,"mission":"ARTEMIS"}}'
```

Read its state and list rockets:

```sh
curl --fail http://localhost:8088/rockets/193270a9-c9cf-404a-8f83-838e71d9ae67
curl --fail 'http://localhost:8088/rockets?sort=speed&order=desc&limit=10&offset=0'
```

Supported events: `RocketLaunched`, `RocketSpeedIncreased`, `RocketSpeedDecreased`, `RocketMissionChanged`, and `RocketExploded`.

| Endpoint | Purpose |
| --- | --- |
| `POST /messages` | Store an event and apply available contiguous events |
| `GET /rockets/{channel}` | Get a rocket's current state |
| `GET /rockets` | List rockets; sort by `channel`, `type`, `speed`, or `mission` |
| `GET /health/live` | Check the HTTP service |
| `GET /health/ready` | Check database connectivity |

Message numbering starts at 1 with a launch event. A rocket appears in read results after launch is applied. Successful ingestion can mean an event is stored pending a gap; responses expose sequence progress and known gaps.

## Useful commands

```sh
docker compose logs -f app  # Follow application logs
docker compose ps -a       # Check service and migration status
make db-clean              # Delete events and snapshots; keep schema/migrations
docker compose down        # Stop the stack; keep database data
```

See [DEVELOPMENT.md](DEVELOPMENT.md) for API parameters, error responses, and local development, and [arch.md](arch.md) for architecture decisions and trade-offs.
