package services

import (
	"context"
	"time"

	"rockets/internal/domain"
)

// EventStore serializes transactions per channel, including first delivery.
// It commits only when fn returns nil and rolls back all changes otherwise.
// The callback must execute exactly once and must not escape the transaction.
type EventStore interface {
	WithRocketTransaction(ctx context.Context, channel string, fn func(RocketTransaction) error) error
}

// RocketTransaction operates on a locked rocket and its immutable event history.
// Snapshot returns a placeholder with sequence 0 for an uninitialized channel.
// Event returns found=false for a missing sequence number. InsertEvent must
// preserve uniqueness of (channel, messageNumber) and never overwrite history.
type RocketTransaction interface {
	Snapshot(context.Context) (domain.Rocket, error)
	Event(ctx context.Context, number int64) (event domain.Event, found bool, err error)
	InsertEvent(ctx context.Context, event domain.Event, receivedAt time.Time) error
	SaveSnapshot(context.Context, domain.Rocket) error
}
