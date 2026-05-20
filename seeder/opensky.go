package seeder

import (
	"context"
	"encoding/base64"
	"fmt"
	"io"
	"net/http"
	"os"
	"time"

	"github.com/jackweekly/OSINT/entity"
	"github.com/tidwall/gjson"
)

const openSkyURL = "https://opensky-network.org/api/states/all"

// OpenSkySeeder fetches live aircraft data from OpenSky Network.
type OpenSkySeeder struct {
	client       *http.Client
	username     string
	password     string
	authenticated bool
	interval     time.Duration
}

// NewOpenSkySeeder creates a new OpenSky seeder.
// Reads OPENSKY_USER and OPENSKY_PASS env vars for authentication.
func NewOpenSkySeeder() *OpenSkySeeder {
	username := os.Getenv("OPENSKY_USER")
	password := os.Getenv("OPENSKY_PASS")
	authenticated := username != "" && password != ""

	interval := 10 * time.Second
	if authenticated {
		interval = 5 * time.Second
	}

	return &OpenSkySeeder{
		client:        &http.Client{Timeout: 10 * time.Second},
		username:      username,
		password:      password,
		authenticated: authenticated,
		interval:      interval,
	}
}

func (s *OpenSkySeeder) Name() string {
	return "opensky"
}

func (s *OpenSkySeeder) Interval() time.Duration {
	return s.interval
}

// Fetch retrieves aircraft states from OpenSky Network API.
// Field indices in state vector:
// 0: icao24 (aircraft identifier)
// 1: callsign (flight callsign)
// 5: longitude
// 6: latitude
// 7: altitude
// 9: velocity (ground speed)
// 10: heading
func (s *OpenSkySeeder) Fetch(ctx context.Context) ([]entity.Entity, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", openSkyURL, nil)
	if err != nil {
		return nil, err
	}

	if s.authenticated {
		auth := base64.StdEncoding.EncodeToString([]byte(s.username + ":" + s.password))
		req.Header.Set("Authorization", "Basic "+auth)
	}

	resp, err := s.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	// Handle rate limiting
	if resp.StatusCode == http.StatusTooManyRequests {
		retryAfter := 10 * time.Second
		if ra := resp.Header.Get("Retry-After"); ra != "" {
			// Try to parse as duration, default to 10s
			if d, err := time.ParseDuration(ra + "s"); err == nil {
				retryAfter = d
			}
		}
		return nil, &RateLimitError{
			Err:        fmt.Errorf("HTTP 429"),
			RetryAfter: retryAfter,
		}
	}

	if resp.StatusCode >= 500 {
		return nil, &RateLimitError{
			Err:        fmt.Errorf("HTTP %d", resp.StatusCode),
			RetryAfter: 30 * time.Second,
		}
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP %d", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	// Parse JSON using gjson
	states := gjson.GetBytes(body, "states").Array()
	entities := make([]entity.Entity, 0, len(states))

	for _, state := range states {
		arr := state.Array()
		if len(arr) < 11 {
			continue
		}

		// Extract fields using gjson Array access
		icao24 := arr[0].String()
		callsign := arr[1].String()
		lng := arr[5].Float()
		lat := arr[6].Float()
		altitude := arr[7].Float()
		speed := arr[9].Float()
		heading := arr[10].Float()

		// Skip if lat/lng are missing
		if lat == 0 && lng == 0 {
			continue
		}

		entities = append(entities, entity.Entity{
			ID:        icao24,
			Source:    "opensky",
			Lat:       lat,
			Lng:       lng,
			Altitude:  altitude,
			Heading:   heading,
			Speed:     speed,
			CallSign:  callsign,
			UpdatedAt: time.Now().Unix(),
			Version:   0,
		})
	}

	return entities, nil
}
