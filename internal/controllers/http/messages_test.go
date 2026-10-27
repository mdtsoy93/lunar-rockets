package httpapi

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"rockets/internal/domain"
)

type eventHandlerFunc func(context.Context, domain.Event) error

func (f eventHandlerFunc) Handle(ctx context.Context, event domain.Event) error { return f(ctx, event) }

func TestMessages(t *testing.T) {
	envelope := func(kind string, number int, payload string) string {
		return fmt.Sprintf(`{"metadata":{"channel":"a","messageNumber":%d,"messageTime":"2022-02-02T19:39:05.863370123+01:00","messageType":%q},"message":%s}`, number, kind, payload)
	}
	launch := envelope("RocketLaunched", 1, `{"type":"Falcon-9","launchSpeed":500,"mission":"ARTEMIS"}`)
	for _, tc := range []struct {
		name       string
		body       string
		serviceErr error
		status     int
		calls      int
	}{
		{"launch", launch, nil, 200, 1},
		{"increase", envelope("RocketSpeedIncreased", 2, `{"by":3000}`), nil, 200, 1},
		{"decrease", envelope("RocketSpeedDecreased", 3, `{"by":2500}`), nil, 200, 1},
		{"mission", envelope("RocketMissionChanged", 4, `{"newMission":"MIR"}`), nil, 200, 1},
		{"explosion", envelope("RocketExploded", 5, `{"reason":"FAILURE"}`), nil, 200, 1},
		{"zero delta", envelope("RocketSpeedIncreased", 2, `{"by":0}`), nil, 200, 1},
		{"missing delta", envelope("RocketSpeedIncreased", 2, `{}`), nil, 400, 0},
		{"missing launch speed", envelope("RocketLaunched", 1, `{"type":"F","mission":"M"}`), nil, 400, 0},
		{"unknown type", envelope("Other", 2, `{}`), nil, 400, 0},
		{"malformed", `{`, nil, 400, 0},
		{"trailing JSON", launch + `{}`, nil, 400, 0},
		{"large body", strings.Repeat(" ", maxMessageBytes) + launch, nil, 413, 0},
		{"transition", launch, domain.ErrInvalidTransition, 422, 1},
		{"database failure", launch, errors.New("secret database connection string"), 503, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			service := eventHandlerFunc(func(ctx context.Context, e domain.Event) error {
				calls++
				expected := map[domain.EventType]domain.Payload{
					domain.RocketLaunched:       domain.Launch{Type: "Falcon-9", LaunchSpeed: 500, Mission: "ARTEMIS"},
					domain.RocketSpeedIncreased: domain.SpeedIncrease{By: 3000},
					domain.RocketSpeedDecreased: domain.SpeedDecrease{By: 2500},
					domain.RocketMissionChanged: domain.MissionChange{NewMission: "MIR"},
					domain.RocketExploded:       domain.Explosion{Reason: "FAILURE"},
				}
				if tc.name == "zero delta" {
					expected[domain.RocketSpeedIncreased] = domain.SpeedIncrease{By: 0}
				}
				if e.Channel != "a" || e.Payload != expected[e.Payload.EventType()] {
					t.Fatalf("incorrect decoded event: %+v", e)
				}
				if e.MessageTime.Nanosecond() != 863370123 {
					t.Fatal("lost timestamp precision")
				}
				return tc.serviceErr
			})
			handler := NewHandler(func(context.Context) error { return nil }, service, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/messages", strings.NewReader(tc.body)))
			if response.Code != tc.status || calls != tc.calls {
				t.Fatalf("status=%d calls=%d, want status=%d calls=%d; body=%s", response.Code, calls, tc.status, tc.calls, response.Body)
			}
			if strings.Contains(response.Body.String(), "secret") {
				t.Fatal("internal details leaked")
			}
		})
	}
}
