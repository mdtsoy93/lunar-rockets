package domain

import (
	"fmt"
	"math"
	"time"
)

type Rocket struct {
	Channel                  string
	Type                     string
	Speed                    int64
	Mission                  string
	Exploded                 bool
	ExplosionReason          string
	LastAppliedMessageNumber int64
	LastAppliedMessageTime   time.Time
}

// Apply returns a new snapshot; an error never modifies the original state.
// Explosion is sticky, but subsequent telemetry is still applied in sequence.
func (r Rocket) Apply(e Event) (Rocket, error) {
	if err := e.Validate(); err != nil {
		return r, err
	}
	invalid := func(reason string) (Rocket, error) { return r, fmt.Errorf("%w: %s", ErrInvalidTransition, reason) }
	if e.Channel != r.Channel {
		return invalid("channel mismatch")
	}
	if r.LastAppliedMessageNumber == math.MaxInt64 || e.MessageNumber != r.LastAppliedMessageNumber+1 {
		return invalid("event is not next in sequence")
	}
	next := r
	switch p := e.Payload.(type) {
	case Launch:
		next.Type, next.Speed, next.Mission = p.Type, p.LaunchSpeed, p.Mission
	case SpeedIncrease:
		if r.Speed > math.MaxInt64-p.By {
			return invalid("speed overflow")
		}
		next.Speed += p.By
	case SpeedDecrease:
		if r.Speed < math.MinInt64+p.By {
			return invalid("speed underflow")
		}
		next.Speed -= p.By
	case MissionChange:
		next.Mission = p.NewMission
	case Explosion:
		if r.Exploded {
			return invalid("rocket already exploded")
		}
		next.Exploded, next.ExplosionReason = true, p.Reason
	}
	next.LastAppliedMessageNumber = e.MessageNumber
	next.LastAppliedMessageTime = e.MessageTime
	return next, nil
}
