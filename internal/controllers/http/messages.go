package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"time"

	"rockets/internal/domain"
)

const maxMessageBytes = 64 * 1024

type messageRequest struct {
	Metadata struct {
		Channel       string           `json:"channel"`
		MessageNumber int64            `json:"messageNumber"`
		MessageTime   time.Time        `json:"messageTime"`
		MessageType   domain.EventType `json:"messageType"`
	} `json:"metadata"`
	Message json.RawMessage `json:"message"`
}

func ingestHandler(events EventHandler, logger *slog.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		r.Body = http.MaxBytesReader(w, r.Body, maxMessageBytes)
		var request messageRequest
		if err := decodeJSON(r.Body, &request); err != nil {
			var tooLarge *http.MaxBytesError
			if errors.As(err, &tooLarge) {
				writeStatus(w, http.StatusRequestEntityTooLarge, `{"error":"message too large"}`)
			} else {
				writeStatus(w, http.StatusBadRequest, `{"error":"invalid JSON request"}`)
			}
			return
		}

		event, err := request.event()
		if err != nil {
			writeStatus(w, http.StatusBadRequest, `{"error":"invalid event"}`)
			return
		}

		ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
		defer cancel()

		if err := events.Handle(ctx, event); err != nil {
			switch {
			case errors.Is(err, domain.ErrInvalidEvent):
				writeStatus(w, http.StatusBadRequest, `{"error":"invalid event"}`)
			case errors.Is(err, domain.ErrInvalidTransition):
				writeStatus(w, http.StatusUnprocessableEntity, `{"error":"invalid rocket transition"}`)
			default:
				logger.ErrorContext(ctx, "event ingestion failed", "channel", event.Channel, "message_number", event.MessageNumber, "error", err)
				writeStatus(w, http.StatusServiceUnavailable, `{"error":"event ingestion unavailable"}`)
			}
			return
		}
		writeStatus(w, http.StatusOK, `{"status":"accepted"}`)
	}
}

func decodeJSON(reader io.Reader, target any) error {
	decoder := json.NewDecoder(reader)
	if err := decoder.Decode(target); err != nil {
		return err
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		if err != nil {
			return err
		}
		return errors.New("multiple JSON values")
	}
	return nil
}

func (r messageRequest) event() (domain.Event, error) {
	// Numeric pointers distinguish an omitted value from an explicitly valid zero.
	var message struct {
		Type        string `json:"type"`
		LaunchSpeed *int64 `json:"launchSpeed"`
		Mission     string `json:"mission"`
		By          *int64 `json:"by"`
		Reason      string `json:"reason"`
		NewMission  string `json:"newMission"`
	}
	if err := json.Unmarshal(r.Message, &message); err != nil {
		return domain.Event{}, err
	}
	var payload domain.Payload
	switch r.Metadata.MessageType {
	case domain.RocketLaunched:
		if message.LaunchSpeed == nil {
			return domain.Event{}, errors.New("launchSpeed is required")
		}
		payload = domain.Launch{Type: message.Type, LaunchSpeed: *message.LaunchSpeed, Mission: message.Mission}
	case domain.RocketSpeedIncreased, domain.RocketSpeedDecreased:
		if message.By == nil {
			return domain.Event{}, errors.New("by is required")
		}
		if r.Metadata.MessageType == domain.RocketSpeedIncreased {
			payload = domain.SpeedIncrease{By: *message.By}
		} else {
			payload = domain.SpeedDecrease{By: *message.By}
		}
	case domain.RocketExploded:
		payload = domain.Explosion{Reason: message.Reason}
	case domain.RocketMissionChanged:
		payload = domain.MissionChange{NewMission: message.NewMission}
	default:
		return domain.Event{}, fmt.Errorf("unknown message type %q", r.Metadata.MessageType)
	}
	event := domain.Event{Channel: r.Metadata.Channel, MessageNumber: r.Metadata.MessageNumber, MessageTime: r.Metadata.MessageTime, Payload: payload}
	return event, event.Validate()
}
