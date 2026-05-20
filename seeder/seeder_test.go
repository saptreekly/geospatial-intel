package seeder

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/saptreekly/geospatial-intel/entity"
)

type mockSeeder struct {
	name      string
	interval  time.Duration
	fetchFunc func(ctx context.Context) ([]entity.Entity, error)
}

func (m *mockSeeder) Name() string { return m.name }
func (m *mockSeeder) Interval() time.Duration { return m.interval }
func (m *mockSeeder) Fetch(ctx context.Context) ([]entity.Entity, error) {
	return m.fetchFunc(ctx)
}

func TestRun_RateLimitError_RespectsRetryAfter(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	calls := 0
	ms := &mockSeeder{
		name:     "test",
		interval: 10 * time.Millisecond,
		fetchFunc: func(ctx context.Context) ([]entity.Entity, error) {
			calls++
			if calls == 1 {
				// Return a rate limit error with 50ms retry after
				return nil, &RateLimitError{
					Err:        errors.New("too many requests"),
					RetryAfter: 50 * time.Millisecond,
				}
			}
			cancel() // cancel context to stop Run
			return []entity.Entity{}, nil
		},
	}

	start := time.Now()
	Run(ctx, ms, func(e []entity.Entity) {})
	elapsed := time.Since(start)

	if calls != 2 {
		t.Errorf("expected 2 calls, got %d", calls)
	}

	// The first call returned a 50ms retry after. So elapsed time should be at least 50ms.
	if elapsed < 50*time.Millisecond {
		t.Errorf("expected elapsed time to be at least 50ms, got %v", elapsed)
	}
}

func TestRun_GenericError_AppliesBackoff(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	calls := 0
	ms := &mockSeeder{
		name:     "test",
		interval: 10 * time.Millisecond,
		fetchFunc: func(ctx context.Context) ([]entity.Entity, error) {
			calls++
			if calls == 1 {
				return nil, errors.New("some error")
			}
			cancel()
			return []entity.Entity{}, nil
		},
	}

	start := time.Now()
	Run(ctx, ms, func(e []entity.Entity) {})
	elapsed := time.Since(start)

	if calls != 2 {
		t.Errorf("expected 2 calls, got %d", calls)
	}

	// The first call returned a generic error.
	// Initial interval is 10ms.
	// Since it's a generic error, it should apply 10ms * 1.5 = 15ms delay.
	// So elapsed time should be at least 15ms.
	if elapsed < 15*time.Millisecond {
		t.Errorf("expected elapsed time to be at least 15ms, got %v", elapsed)
	}
}
