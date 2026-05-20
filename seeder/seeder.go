package seeder

import (
	"context"
	"errors"
	"log"
	"time"

	"github.com/jackweekly/osint/entity"
)

// Seeder fetches entities from a data source.
type Seeder interface {
	Name() string
	Fetch(ctx context.Context) ([]entity.Entity, error)
	Interval() time.Duration
}

var ErrRateLimit = errors.New("rate limited")

// RateLimitError wraps rate limiting errors for exponential backoff handling.
type RateLimitError struct {
	Err       error
	RetryAfter time.Duration
}

func (e *RateLimitError) Error() string {
	return "rate limited: " + e.Err.Error()
}

func (e *RateLimitError) Unwrap() error {
	return e.Err
}

// Run polls a seeder and calls onUpdate with fresh entities on success.
// Implements exponential backoff on rate limit or error, with max 60s delay.
func Run(ctx context.Context, s Seeder, onUpdate func([]entity.Entity)) {
	delay := s.Interval()
	maxDelay := 60 * time.Second

	log.Printf("Starting seeder: %s (interval: %v)", s.Name(), delay)

	for {
		entities, err := s.Fetch(ctx)
		if err != nil {
			var rateLimitErr *RateLimitError
			if errors.As(err, &rateLimitErr) {
				log.Printf("Seeder %s rate limited: %v. Retrying in %v", s.Name(), err, delay)
				if delay < maxDelay {
					delay = time.Duration(float64(delay) * 1.5)
					if delay > maxDelay {
						delay = maxDelay
					}
				}
			} else {
				log.Printf("Seeder %s error: %v. Retrying in %v", s.Name(), err, delay)
			}
			select {
			case <-time.After(delay):
				continue
			case <-ctx.Done():
				return
			}
		}

		log.Printf("Seeder %s fetched %d entities", s.Name(), len(entities))
		delay = s.Interval() // reset on success
		onUpdate(entities)

		select {
		case <-time.After(delay):
		case <-ctx.Done():
			return
		}
	}
}
