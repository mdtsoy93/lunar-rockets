package services

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"rockets/internal/domain"
)

var (
	ErrRocketNotFound = errors.New("rocket not found")
	ErrInvalidQuery   = errors.New("invalid rocket query")
)

type RocketState struct {
	Rocket              domain.Rocket
	PendingMessageCount int64
}

type ListOptions struct {
	Sort   string
	Order  string
	Limit  int
	Offset int
}

func (o ListOptions) Validate() error {
	switch o.Sort {
	case "channel", "type", "speed", "mission":
	default:
		return fmt.Errorf("%w: unsupported sort field", ErrInvalidQuery)
	}
	if o.Order != "asc" && o.Order != "desc" {
		return fmt.Errorf("%w: order must be asc or desc", ErrInvalidQuery)
	}
	if o.Limit < 1 || o.Limit > 1000 || o.Offset < 0 {
		return fmt.Errorf("%w: limit must be 1..1000 and offset nonnegative", ErrInvalidQuery)
	}
	return nil
}

// RocketReader returns only initialized rockets. Implementations must read each
// snapshot and its pending count consistently within the same database snapshot.
type RocketReader interface {
	GetRocket(context.Context, string) (RocketState, error)
	ListRockets(context.Context, ListOptions) ([]RocketState, error)
}

type QueryService struct{ reader RocketReader }

func NewQueryService(reader RocketReader) *QueryService { return &QueryService{reader: reader} }

func (s *QueryService) GetRocket(ctx context.Context, channel string) (RocketState, error) {
	if strings.TrimSpace(channel) == "" {
		return RocketState{}, fmt.Errorf("%w: channel is required", ErrInvalidQuery)
	}
	return s.reader.GetRocket(ctx, channel)
}

func (s *QueryService) ListRockets(ctx context.Context, options ListOptions) ([]RocketState, error) {
	if err := options.Validate(); err != nil {
		return nil, err
	}
	return s.reader.ListRockets(ctx, options)
}
