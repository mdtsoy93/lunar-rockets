package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"rockets/internal/services"
)

type RocketQueries interface {
	GetRocket(context.Context, string) (services.RocketState, error)
	ListRockets(context.Context, services.ListOptions) ([]services.RocketState, error)
}

type rocketResponse struct {
	Channel                  string    `json:"channel"`
	Type                     string    `json:"type"`
	Speed                    int64     `json:"speed"`
	Mission                  string    `json:"mission"`
	Exploded                 bool      `json:"exploded"`
	ExplosionReason          string    `json:"explosionReason,omitempty"`
	LastAppliedMessageNumber int64     `json:"lastAppliedMessageNumber"`
	LastAppliedMessageTime   time.Time `json:"lastAppliedMessageTime"`
	PendingMessageCount      int64     `json:"pendingMessageCount"`
	HasKnownGaps             bool      `json:"hasKnownGaps"`
}

func toRocketResponse(s services.RocketState) rocketResponse {
	r := s.Rocket
	return rocketResponse{Channel: r.Channel, Type: r.Type, Speed: r.Speed, Mission: r.Mission,
		Exploded: r.Exploded, ExplosionReason: r.ExplosionReason, LastAppliedMessageNumber: r.LastAppliedMessageNumber,
		LastAppliedMessageTime: r.LastAppliedMessageTime, PendingMessageCount: s.PendingMessageCount, HasKnownGaps: s.PendingMessageCount > 0}
}

func getRocketHandler(queries RocketQueries, logger *slog.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		defer cancel()
		state, err := queries.GetRocket(ctx, r.PathValue("channel"))
		if err != nil {
			writeQueryError(ctx, w, logger, err)
			return
		}
		writeJSON(w, toRocketResponse(state))
	}
}

func listRocketsHandler(queries RocketQueries, logger *slog.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		options, err := parseListOptions(r.URL.RawQuery)
		if err != nil {
			writeStatus(w, http.StatusBadRequest, `{"error":"invalid query parameters"}`)
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		defer cancel()
		states, err := queries.ListRockets(ctx, options)
		if err != nil {
			writeQueryError(ctx, w, logger, err)
			return
		}
		rockets := make([]rocketResponse, 0, len(states))
		for _, state := range states {
			rockets = append(rockets, toRocketResponse(state))
		}
		writeJSON(w, struct {
			Rockets []rocketResponse `json:"rockets"`
			Limit   int              `json:"limit"`
			Offset  int              `json:"offset"`
		}{rockets, options.Limit, options.Offset})
	}
}

func parseListOptions(rawQuery string) (services.ListOptions, error) {
	o := services.ListOptions{Sort: "channel", Order: "asc", Limit: 100}
	values, err := url.ParseQuery(rawQuery)
	if err != nil {
		return o, err
	}
	for key, items := range values {
		if len(items) != 1 {
			return o, services.ErrInvalidQuery
		}
		switch key {
		case "sort":
			o.Sort = items[0]
		case "order":
			o.Order = items[0]
		case "limit":
			o.Limit, err = strconv.Atoi(items[0])
		case "offset":
			o.Offset, err = strconv.Atoi(items[0])
		default:
			return o, services.ErrInvalidQuery
		}
		if err != nil {
			return o, err
		}
	}
	return o, o.Validate()
}

func writeQueryError(ctx context.Context, w http.ResponseWriter, logger *slog.Logger, err error) {
	switch {
	case errors.Is(err, services.ErrRocketNotFound):
		writeStatus(w, http.StatusNotFound, `{"error":"rocket not found"}`)
	case errors.Is(err, services.ErrInvalidQuery):
		writeStatus(w, http.StatusBadRequest, `{"error":"invalid query parameters"}`)
	default:
		logger.ErrorContext(ctx, "rocket query failed", "error", err)
		writeStatus(w, http.StatusServiceUnavailable, `{"error":"rocket query unavailable"}`)
	}
}

func writeJSON(w http.ResponseWriter, value any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(value)
}
