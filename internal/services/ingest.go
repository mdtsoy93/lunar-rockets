package services

import (
	"context"
	"fmt"
	"math"
	"time"

	"rockets/internal/domain"
)

type IngestService struct{ store EventStore }

func NewIngestService(store EventStore) *IngestService { return &IngestService{store: store} }

// Handle durably stores an event and advances the snapshot through all available
// contiguous events. Success is returned only after the transaction commits.
func (s *IngestService) Handle(ctx context.Context, event domain.Event) error {
	if err := event.Validate(); err != nil {
		return err
	}
	receivedAt := time.Now().UTC()
	err := s.store.WithRocketTransaction(ctx, event.Channel, func(tx RocketTransaction) error {
		_, found, err := tx.Event(ctx, event.MessageNumber)
		if err != nil {
			return fmt.Errorf("find existing event: %w", err)
		}
		if found {
			return nil
		}
		if err := tx.InsertEvent(ctx, event, receivedAt); err != nil {
			return fmt.Errorf("insert event: %w", err)
		}
		rocket, err := tx.Snapshot(ctx)
		if err != nil {
			return fmt.Errorf("load snapshot: %w", err)
		}
		return applyPendingEvents(ctx, tx, rocket)
	})
	if err != nil {
		return fmt.Errorf("ingest event: %w", err)
	}
	return nil
}

func applyPendingEvents(ctx context.Context, tx RocketTransaction, rocket domain.Rocket) error {
	previous := rocket.LastAppliedMessageNumber
	for rocket.LastAppliedMessageNumber < math.MaxInt64 {
		if err := ctx.Err(); err != nil {
			return err
		}
		next, found, err := tx.Event(ctx, rocket.LastAppliedMessageNumber+1)
		if err != nil {
			return fmt.Errorf("load next event: %w", err)
		}
		if !found {
			break
		}
		rocket, err = rocket.Apply(next)
		if err != nil {
			return fmt.Errorf("apply message %d: %w", next.MessageNumber, err)
		}
	}
	if rocket.LastAppliedMessageNumber == previous {
		return nil
	}
	if err := tx.SaveSnapshot(ctx, rocket); err != nil {
		return fmt.Errorf("save snapshot: %w", err)
	}
	return nil
}
