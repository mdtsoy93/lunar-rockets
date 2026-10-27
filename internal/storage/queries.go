package storage

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"rockets/internal/services"
)

var _ services.RocketReader = (*Store)(nil)

const rocketStateColumns = `r.channel, r.rocket_type, r.speed, r.mission, r.exploded,
    r.explosion_reason, r.last_applied_message_number, r.last_applied_message_time,
    (SELECT count(*) FROM rocket_events e WHERE e.channel = r.channel
        AND e.message_number > r.last_applied_message_number)`

func (s *Store) GetRocket(ctx context.Context, channel string) (services.RocketState, error) {
	state, err := scanRocketState(s.pool.QueryRow(ctx, `SELECT `+rocketStateColumns+`
        FROM rockets r WHERE r.channel=$1 AND r.last_applied_message_number > 0`, channel))
	if errors.Is(err, pgx.ErrNoRows) {
		return services.RocketState{}, services.ErrRocketNotFound
	}
	if err != nil {
		return services.RocketState{}, fmt.Errorf("get rocket: %w", err)
	}
	return state, nil
}

func (s *Store) ListRockets(ctx context.Context, options services.ListOptions) ([]services.RocketState, error) {
	if err := options.Validate(); err != nil {
		return nil, err
	}
	// Only allowlisted identifiers enter SQL; all pagination values are parameters.
	columns := map[string]string{"channel": "r.channel", "type": "r.rocket_type", "speed": "r.speed", "mission": "r.mission"}
	direction := "ASC"
	if options.Order == "desc" {
		direction = "DESC"
	}
	rows, err := s.pool.Query(ctx, `SELECT `+rocketStateColumns+` FROM rockets r
        WHERE r.last_applied_message_number > 0 ORDER BY `+columns[options.Sort]+` `+direction+`, r.channel ASC LIMIT $1 OFFSET $2`, options.Limit, options.Offset)
	if err != nil {
		return nil, fmt.Errorf("list rockets: %w", err)
	}
	defer rows.Close()
	states := make([]services.RocketState, 0)
	for rows.Next() {
		state, err := scanRocketState(rows)
		if err != nil {
			return nil, fmt.Errorf("scan rocket: %w", err)
		}
		states = append(states, state)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read rockets: %w", err)
	}
	return states, nil
}

func scanRocketState(row interface{ Scan(...any) error }) (services.RocketState, error) {
	var state services.RocketState
	r := &state.Rocket
	var timestamp string
	err := row.Scan(&r.Channel, &r.Type, &r.Speed, &r.Mission, &r.Exploded, &r.ExplosionReason,
		&r.LastAppliedMessageNumber, &timestamp, &state.PendingMessageCount)
	if err != nil {
		return state, err
	}
	r.LastAppliedMessageTime, err = time.Parse(time.RFC3339Nano, timestamp)
	return state, err
}
