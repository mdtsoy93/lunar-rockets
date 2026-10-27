package storage

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	httpapi "rockets/internal/controllers/http"
	"rockets/internal/domain"
	"rockets/internal/services"
)

func TestPostgresEventStore(t *testing.T) {
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("set TEST_DATABASE_URL to run PostgreSQL integration tests")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	admin, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close()
	schema := pgx.Identifier{fmt.Sprintf("rocket_test_%d", time.Now().UnixNano())}.Sanitize()
	if _, err := admin.Exec(ctx, "CREATE SCHEMA "+schema); err != nil {
		t.Fatal(err)
	}
	defer func() {
		cleanupCtx, stop := context.WithTimeout(context.Background(), 5*time.Second)
		defer stop()
		if _, err := admin.Exec(cleanupCtx, "DROP SCHEMA "+schema+" CASCADE"); err != nil {
			t.Error(err)
		}
	}()
	cfg, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	cfg.ConnConfig.RuntimeParams["search_path"] = schema
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	store := &Store{pool: pool}
	applyMigration := func(name string) {
		t.Helper()
		data, err := os.ReadFile(filepath.Join("..", "..", "migrations", name))
		if err != nil {
			t.Fatal(err)
		}
		if _, err := pool.Exec(ctx, string(data)); err != nil {
			t.Fatal(err)
		}
	}
	applyMigration("000001_baseline.up.sql")
	applyMigration("000002_rockets.up.sql")
	service := services.NewIngestService(store)
	event := func(channel string, n int64, payload domain.Payload) domain.Event {
		return domain.Event{Channel: channel, MessageNumber: n, MessageTime: time.Date(2022, 2, 2, 12, 0, 0, 123456789, time.UTC), Payload: payload}
	}
	launch := domain.Launch{Type: "Falcon-9", LaunchSpeed: 500, Mission: "ARTEMIS"}
	readSnapshot := func(channel string) domain.Rocket {
		t.Helper()
		var snapshot domain.Rocket
		if err := store.WithRocketTransaction(ctx, channel, func(tx services.RocketTransaction) error {
			var err error
			snapshot, err = tx.Snapshot(ctx)
			return err
		}); err != nil {
			t.Fatal(err)
		}
		return snapshot
	}
	t.Run("shuffled history survives reconnect and deduplicates precise timestamps", func(t *testing.T) {
		events := []domain.Event{
			event("shuffled", 1, launch), event("shuffled", 2, domain.SpeedIncrease{By: 3000}),
			event("shuffled", 3, domain.SpeedDecrease{By: 2500}), event("shuffled", 4, domain.MissionChange{NewMission: "MIR"}),
			event("shuffled", 5, domain.Explosion{Reason: "FAILURE"}),
		}
		if err := service.Handle(ctx, events[2]); err != nil {
			t.Fatal(err)
		}
		if r := readSnapshot("shuffled"); r.LastAppliedMessageNumber != 0 {
			t.Fatal("gap advanced snapshot")
		}
		// A separate connection pool sees the committed pending history.
		otherPool, err := pgxpool.NewWithConfig(ctx, cfg.Copy())
		if err != nil {
			t.Fatal(err)
		}
		other := services.NewIngestService(&Store{pool: otherPool})
		defer otherPool.Close()
		for _, i := range []int{4, 3, 0, 1} {
			if err := other.Handle(ctx, events[i]); err != nil {
				t.Fatal(err)
			}
		}
		for _, e := range events {
			if err := service.Handle(ctx, e); err != nil {
				t.Fatal(err)
			}
		}
		r := readSnapshot("shuffled")
		if r.Speed != 1000 || r.Mission != "MIR" || !r.Exploded || r.LastAppliedMessageNumber != 5 || !r.LastAppliedMessageTime.Equal(events[4].MessageTime) {
			t.Fatalf("bad snapshot: %+v", r)
		}
		var count int
		if err := pool.QueryRow(ctx, `SELECT count(*) FROM rocket_events WHERE channel='shuffled'`).Scan(&count); err != nil || count != 5 {
			t.Fatalf("history count %d: %v", count, err)
		}

	})
	t.Run("concurrent first delivery and duplicates", func(t *testing.T) {
		var wg sync.WaitGroup
		errs := make(chan error, 40)
		for i := 0; i < 40; i++ {
			wg.Add(1)
			go func(i int) {
				defer wg.Done()
				n := int64(i%20 + 1)
				var payload domain.Payload = domain.SpeedIncrease{By: 1}
				if n == 1 {
					payload = launch
				}
				errs <- service.Handle(ctx, event("concurrent", n, payload))
			}(i)
		}
		wg.Wait()
		close(errs)
		for err := range errs {
			if err != nil {
				t.Fatal(err)
			}
		}
		if r := readSnapshot("concurrent"); r.LastAppliedMessageNumber != 20 || r.Speed != 519 {
			t.Fatalf("bad concurrent result: %+v", r)
		}
	})
	t.Run("callback failure rolls back event snapshot and placeholder", func(t *testing.T) {
		failure := errors.New("abort")
		err := store.WithRocketTransaction(ctx, "rollback", func(tx services.RocketTransaction) error {
			e := event("rollback", 1, launch)
			if err := tx.InsertEvent(ctx, e, time.Now()); err != nil {
				return err
			}
			r, err := (domain.Rocket{Channel: "rollback"}).Apply(e)
			if err != nil {
				return err
			}
			if err := tx.SaveSnapshot(ctx, r); err != nil {
				return err
			}
			return failure
		})
		if !errors.Is(err, failure) {
			t.Fatal(err)
		}
		var count int
		if err := pool.QueryRow(ctx, `SELECT count(*) FROM rockets WHERE channel='rollback'`).Scan(&count); err != nil || count != 0 {
			t.Fatalf("rollback leaked data: %d, %v", count, err)
		}
	})
	t.Run("HTTP service database wiring", func(t *testing.T) {
		handler := httpapi.NewHandler(store.Ping, service, services.NewQueryService(store), slog.New(slog.NewTextHandler(io.Discard, nil)))
		body := `{"metadata":{"channel":"http-rocket","messageNumber":1,"messageTime":"2022-02-02T12:00:00.123456789Z","messageType":"RocketLaunched"},"message":{"type":"Falcon-9","launchSpeed":500,"mission":"ARTEMIS"}}`
		for i := 0; i < 2; i++ {
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/messages", strings.NewReader(body)))
			if response.Code != http.StatusOK {
				t.Fatalf("ingest returned %d: %s", response.Code, response.Body)
			}
		}
		if r := readSnapshot("http-rocket"); r.Speed != 500 || r.LastAppliedMessageNumber != 1 {
			t.Fatalf("bad HTTP snapshot: %+v", r)
		}
	})
	t.Run("read APIs sorting pagination gaps and missing rockets", func(t *testing.T) {
		if _, err := pool.Exec(ctx, "TRUNCATE rocket_events, rockets"); err != nil {
			t.Fatal(err)
		}
		for _, seed := range []struct {
			channel    string
			speed      int64
			rocketType string
			mission    string
		}{
			{"read-a", 100, "A", "X"}, {"read-b", 100, "B", "Y"}, {"read-c", 200, "C", "Z"},
		} {
			if err := service.Handle(ctx, event(seed.channel, 1, domain.Launch{Type: seed.rocketType, LaunchSpeed: seed.speed, Mission: seed.mission})); err != nil {
				t.Fatal(err)
			}
		}
		if err := service.Handle(ctx, event("uninitialized", 3, domain.SpeedIncrease{By: 1})); err != nil {
			t.Fatal(err)
		}
		if err := service.Handle(ctx, event("read-a", 3, domain.SpeedIncrease{By: 1})); err != nil {
			t.Fatal(err)
		}
		queries := services.NewQueryService(store)
		state, err := queries.GetRocket(ctx, "read-a")
		if err != nil || state.PendingMessageCount != 1 || state.Rocket.LastAppliedMessageNumber != 1 || state.Rocket.Speed != 100 {
			t.Fatalf("bad pending state: %+v, %v", state, err)
		}
		for _, channel := range []string{"missing", "uninitialized"} {
			if _, err := queries.GetRocket(ctx, channel); !errors.Is(err, services.ErrRocketNotFound) {
				t.Fatalf("%s: %v", channel, err)
			}
		}
		for _, tc := range []struct {
			sort   string
			order  string
			offset int
			want   []string
		}{
			{"speed", "asc", 0, []string{"read-a", "read-b"}},
			{"speed", "asc", 1, []string{"read-b", "read-c"}},
			{"speed", "desc", 0, []string{"read-c", "read-a"}},
			{"channel", "asc", 0, []string{"read-a", "read-b"}},
			{"channel", "desc", 0, []string{"read-c", "read-b"}},
			{"type", "asc", 0, []string{"read-a", "read-b"}},
			{"mission", "desc", 0, []string{"read-c", "read-b"}},
			{"channel", "asc", 100, nil},
		} {
			states, err := queries.ListRockets(ctx, services.ListOptions{Sort: tc.sort, Order: tc.order, Limit: 2, Offset: tc.offset})
			if err != nil {
				t.Fatal(err)
			}
			if len(states) != len(tc.want) {
				t.Fatalf("%+v: got %d results", tc, len(states))
			}
			for i, channel := range tc.want {
				if states[i].Rocket.Channel != channel {
					t.Fatalf("%+v: got %s at %d", tc, states[i].Rocket.Channel, i)
				}
			}
		}
		if err := service.Handle(ctx, event("read-a", 2, domain.SpeedIncrease{By: 1})); err != nil {
			t.Fatal(err)
		}
		state, err = queries.GetRocket(ctx, "read-a")
		if err != nil || state.PendingMessageCount != 0 || state.Rocket.Speed != 102 {
			t.Fatalf("bad gap closure: %+v %v", state, err)
		}
		handler := httpapi.NewHandler(store.Ping, service, queries, slog.Default())
		get := func(path string, status int, target any) {
			t.Helper()
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, path, nil))
			if response.Code != status {
				t.Fatalf("%s: status=%d body=%s", path, response.Code, response.Body)
			}
			if target != nil {
				if err := json.Unmarshal(response.Body.Bytes(), target); err != nil {
					t.Fatal(err)
				}
			}
		}
		var rocket struct {
			Channel                  string
			Type                     string
			Speed                    int64
			Mission                  string
			LastAppliedMessageNumber int64
			LastAppliedMessageTime   time.Time
			PendingMessageCount      int64
			HasKnownGaps             bool
		}
		get("/rockets/read-a", http.StatusOK, &rocket)
		if rocket.Channel != "read-a" || rocket.Type != "A" || rocket.Speed != 102 || rocket.Mission != "X" || rocket.LastAppliedMessageNumber != 3 || rocket.PendingMessageCount != 0 || rocket.HasKnownGaps || rocket.LastAppliedMessageTime.Nanosecond() != 123456789 {
			t.Fatalf("incorrect HTTP rocket: %+v", rocket)
		}
		var page struct {
			Rockets []struct{ Channel string }
			Limit   int
			Offset  int
		}
		get("/rockets?sort=speed&order=desc&limit=1&offset=1", http.StatusOK, &page)
		if len(page.Rockets) != 1 || page.Rockets[0].Channel != "read-a" || page.Limit != 1 || page.Offset != 1 {
			t.Fatalf("incorrect HTTP page: %+v", page)
		}
		get("/rockets?offset=100", http.StatusOK, &page)
		if page.Rockets == nil || len(page.Rockets) != 0 {
			t.Fatalf("expected empty array: %+v", page)
		}
		for _, path := range []string{"/rockets/missing", "/rockets/uninitialized"} {
			get(path, http.StatusNotFound, nil)
		}
		for _, query := range []string{"sort=unknown", "order=invalid", "limit=1001", "offset=-1", "limit=abc", "sort=speed&sort=mission"} {
			get("/rockets?"+query, http.StatusBadRequest, nil)
		}
		var count int
		if err := pool.QueryRow(ctx, `SELECT count(*) FROM rockets WHERE channel='missing'`).Scan(&count); err != nil || count != 0 {
			t.Fatalf("read created placeholder: %d %v", count, err)
		}
	})
	applyMigration("000002_rockets.down.sql")
	applyMigration("000002_rockets.up.sql")
}
