package domain

import (
	"errors"
	"fmt"
	"strings"
	"time"
)

var (
	ErrInvalidEvent      = errors.New("invalid event")
	ErrInvalidTransition = errors.New("invalid rocket transition")
)

type EventType string

const (
	RocketLaunched       EventType = "RocketLaunched"
	RocketSpeedIncreased EventType = "RocketSpeedIncreased"
	RocketSpeedDecreased EventType = "RocketSpeedDecreased"
	RocketExploded       EventType = "RocketExploded"
	RocketMissionChanged EventType = "RocketMissionChanged"
)

// Payload is sealed to the event types understood by the domain. Use value
// payloads, not pointers, as required by event validation.
type Payload interface {
	EventType() EventType
	eventPayload()
}

type Launch struct {
	Type        string
	LaunchSpeed int64
	Mission     string
}

func (Launch) EventType() EventType { return RocketLaunched }
func (Launch) eventPayload()        {}

type SpeedIncrease struct{ By int64 }

func (SpeedIncrease) EventType() EventType { return RocketSpeedIncreased }
func (SpeedIncrease) eventPayload()        {}

type SpeedDecrease struct{ By int64 }

func (SpeedDecrease) EventType() EventType { return RocketSpeedDecreased }
func (SpeedDecrease) eventPayload()        {}

type Explosion struct{ Reason string }

func (Explosion) EventType() EventType { return RocketExploded }
func (Explosion) eventPayload()        {}

type MissionChange struct{ NewMission string }

func (MissionChange) EventType() EventType { return RocketMissionChanged }
func (MissionChange) eventPayload()        {}

type Event struct {
	Channel       string
	MessageNumber int64
	MessageTime   time.Time
	Payload       Payload
}

func (e Event) Validate() error {
	invalid := func(reason string) error { return fmt.Errorf("%w: %s", ErrInvalidEvent, reason) }
	if strings.TrimSpace(e.Channel) == "" {
		return invalid("channel is required")
	}
	if e.MessageNumber < 1 {
		return invalid("message number must be positive")
	}
	if e.MessageTime.IsZero() {
		return invalid("message time is required")
	}
	switch p := e.Payload.(type) {
	case Launch:
		if e.MessageNumber != 1 {
			return invalid("launch must be message 1")
		}
		if strings.TrimSpace(p.Type) == "" || strings.TrimSpace(p.Mission) == "" || p.LaunchSpeed < 0 {
			return invalid("launch requires type, mission, and nonnegative speed")
		}
	case SpeedIncrease:
		if p.By < 0 {
			return invalid("speed increase must be nonnegative")
		}
	case SpeedDecrease:
		if p.By < 0 {
			return invalid("speed decrease must be nonnegative")
		}
	case Explosion:
		if strings.TrimSpace(p.Reason) == "" {
			return invalid("explosion reason is required")
		}
	case MissionChange:
		if strings.TrimSpace(p.NewMission) == "" {
			return invalid("mission is required")
		}
	default:
		return invalid("unsupported payload")
	}
	if e.MessageNumber == 1 && e.Payload.EventType() != RocketLaunched {
		return invalid("message 1 must be a launch")
	}
	return nil
}
