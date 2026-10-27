package storage

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"rockets/internal/domain"
	"rockets/internal/services"
)

var _ services.EventStore = (*Store)(nil)

type rocketTransaction struct {
	tx      pgx.Tx
	channel string
}

func (s *Store) WithRocketTransaction(ctx context.Context, channel string, fn func(services.RocketTransaction) error) error {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted})
	if err != nil {
		return fmt.Errorf("begin rocket transaction: %w", err)
	}
	defer func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = tx.Rollback(cleanupCtx)
	}()
	if _, err := tx.Exec(ctx, `INSERT INTO rockets (channel) VALUES ($1) ON CONFLICT (channel) DO NOTHING`, channel); err != nil {
		return fmt.Errorf("ensure rocket: %w", err)
	}
	var locked string
	if err := tx.QueryRow(ctx, `SELECT channel FROM rockets WHERE channel = $1 FOR UPDATE`, channel).Scan(&locked); err != nil {
		return fmt.Errorf("lock rocket: %w", err)
	}
	if err := fn(&rocketTransaction{tx: tx, channel: channel}); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit rocket transaction: %w", err)
	}
	return nil
}

func (t *rocketTransaction) Snapshot(ctx context.Context) (domain.Rocket, error) {
	r := domain.Rocket{Channel: t.channel}
	var timestamp *string
	err := t.tx.QueryRow(ctx, `SELECT rocket_type, speed, mission, exploded, explosion_reason,
        last_applied_message_number, last_applied_message_time FROM rockets WHERE channel = $1`, t.channel).
		Scan(&r.Type, &r.Speed, &r.Mission, &r.Exploded, &r.ExplosionReason, &r.LastAppliedMessageNumber, &timestamp)
	if err != nil {
		return r, err
	}
	if timestamp != nil {
		r.LastAppliedMessageTime, err = time.Parse(time.RFC3339Nano, *timestamp)
	}
	return r, err
}

func (t *rocketTransaction) Event(ctx context.Context, number int64) (domain.Event, bool, error) {
	e := domain.Event{Channel: t.channel, MessageNumber: number}
	var kind domain.EventType
	var timestamp string
	var payload []byte
	err := t.tx.QueryRow(ctx, `SELECT message_type, message_time_exact, payload FROM rocket_events
        WHERE channel = $1 AND message_number = $2`, t.channel, number).Scan(&kind, &timestamp, &payload)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.Event{}, false, nil
	}
	if err != nil {
		return domain.Event{}, false, err
	}
	e.MessageTime, err = time.Parse(time.RFC3339Nano, timestamp)
	if err != nil {
		return domain.Event{}, false, fmt.Errorf("decode event time: %w", err)
	}
	e.Payload, err = decodePayload(kind, payload)
	if err != nil {
		return domain.Event{}, false, err
	}
	if err := e.Validate(); err != nil {
		return domain.Event{}, false, fmt.Errorf("invalid stored event: %w", err)
	}
	return e, true, nil
}

func (t *rocketTransaction) InsertEvent(ctx context.Context, e domain.Event, receivedAt time.Time) error {
	if e.Channel != t.channel {
		return errors.New("event channel differs from locked rocket")
	}
	if err := e.Validate(); err != nil {
		return err
	}
	payload, err := json.Marshal(e.Payload)
	if err != nil {
		return err
	}
	_, err = t.tx.Exec(ctx, `INSERT INTO rocket_events
        (channel, message_number, message_type, message_time, message_time_exact, payload, received_at)
        VALUES ($1, $2, $3, $4, $5, $6, $7)`, e.Channel, e.MessageNumber, e.Payload.EventType(),
		e.MessageTime, e.MessageTime.UTC().Format(time.RFC3339Nano), payload, receivedAt)
	return err
}

func (t *rocketTransaction) SaveSnapshot(ctx context.Context, r domain.Rocket) error {
	if r.Channel != t.channel {
		return errors.New("snapshot channel differs from locked rocket")
	}
	var timestamp *string
	if !r.LastAppliedMessageTime.IsZero() {
		value := r.LastAppliedMessageTime.UTC().Format(time.RFC3339Nano)
		timestamp = &value
	}
	_, err := t.tx.Exec(ctx, `UPDATE rockets SET rocket_type=$2, speed=$3, mission=$4, exploded=$5,
        explosion_reason=$6, last_applied_message_number=$7, last_applied_message_time=$8 WHERE channel=$1`,
		r.Channel, r.Type, r.Speed, r.Mission, r.Exploded, r.ExplosionReason, r.LastAppliedMessageNumber, timestamp)
	return err
}

func decodePayload(kind domain.EventType, data []byte) (domain.Payload, error) {
	// Decode pointers locally, then return value payloads expected by the domain.
	decode := func(target any) error { return json.Unmarshal(data, target) }
	switch kind {
	case domain.RocketLaunched:
		var p domain.Launch
		err := decode(&p)
		return p, err
	case domain.RocketSpeedIncreased:
		var p domain.SpeedIncrease
		err := decode(&p)
		return p, err
	case domain.RocketSpeedDecreased:
		var p domain.SpeedDecrease
		err := decode(&p)
		return p, err
	case domain.RocketExploded:
		var p domain.Explosion
		err := decode(&p)
		return p, err
	case domain.RocketMissionChanged:
		var p domain.MissionChange
		err := decode(&p)
		return p, err
	default:
		return nil, fmt.Errorf("unknown stored event type %q", kind)
	}
}
