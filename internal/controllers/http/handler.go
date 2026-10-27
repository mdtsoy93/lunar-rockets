package httpapi

import (
	"context"
	"log/slog"
	"net/http"
	"time"

	"rockets/internal/domain"
)

type EventHandler interface {
	Handle(context.Context, domain.Event) error
}

func NewHandler(checkDatabase func(context.Context) error, events EventHandler, queries RocketQueries, logger *slog.Logger) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /messages", ingestHandler(events, logger))
	mux.HandleFunc("GET /rockets/{channel}", getRocketHandler(queries, logger))
	mux.HandleFunc("GET /rockets", listRocketsHandler(queries, logger))
	mux.HandleFunc("GET /health/live", func(w http.ResponseWriter, r *http.Request) {
		writeStatus(w, http.StatusOK, `{"status":"ok"}`)
	})
	mux.HandleFunc("GET /health/ready", func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
		defer cancel()
		if err := checkDatabase(ctx); err != nil {
			writeStatus(w, http.StatusServiceUnavailable, `{"status":"unavailable"}`)
			return
		}
		writeStatus(w, http.StatusOK, `{"status":"ok"}`)
	})
	return mux
}

func writeStatus(w http.ResponseWriter, status int, body string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = w.Write([]byte(body + "\n"))
}
