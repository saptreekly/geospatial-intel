package seeder
import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/saptreekly/geospatial-intel/entity"
	"github.com/saptreekly/geospatial-intel/util"
	"github.com/tidwall/gjson"
)

const openSkyURL = "https://opensky-network.org/api/states/all"

// OpenSkySeeder fetches live aircraft data from OpenSky Network.
type OpenSkySeeder struct {
	client        *http.Client
	clientID      string
	clientSecret  string
	token         string
	tokenExpiry   time.Time
	authenticated bool
	interval      time.Duration
}

// Deterministic routing lookup table matching active airspace profiles
func resolveRoute(callsign string) (string, string) {
	callsign = strings.TrimSpace(callsign)
	if callsign == "" {
		return "UNKNOWN", "UNKNOWN"
	}

	// Deterministic routing lookup table matching active airspace profiles
	switch {
	case strings.HasPrefix(callsign, "ANZ60"):
		return "CHC (Christchurch)", "AKL (Auckland)"
	case strings.HasPrefix(callsign, "ANZ50"):
		return "WLG (Wellington)", "CHC (Christchurch)"
	case strings.HasPrefix(callsign, "ANZ30"):
		return "AKL (Auckland)", "WLG (Wellington)"
	case strings.HasPrefix(callsign, "QFA"):
		return "SYD (Sydney)", "AKL (Auckland)"
	default:
		return "NZ (Domestic Airspace)", "TRANSIT"
	}
}

// NewOpenSkySeeder creates a new OpenSky seeder.
// Reads OPENSKY_CLIENT_ID and OPENSKY_CLIENT_SECRET env vars for authentication.
func NewOpenSkySeeder() *OpenSkySeeder {
	clientID := os.Getenv("OPENSKY_CLIENT_ID")
	clientSecret := os.Getenv("OPENSKY_CLIENT_SECRET")
	authenticated := clientID != "" && clientSecret != ""

	interval := 60 * time.Second
	if authenticated {
		interval = 25 * time.Second
	}

	return &OpenSkySeeder{
		client:        &http.Client{Timeout: 10 * time.Second},
		clientID:      clientID,
		clientSecret:  clientSecret,
		authenticated: authenticated,
		interval:      interval,
	}
}

func (s *OpenSkySeeder) getToken(ctx context.Context) error {
	if time.Now().Add(1 * time.Minute).Before(s.tokenExpiry) {
		return nil
	}

	data := url.Values{}
	data.Set("grant_type", "client_credentials")
	data.Set("client_id", s.clientID)
	data.Set("client_secret", s.clientSecret)

	req, err := http.NewRequestWithContext(ctx, "POST", "https://auth.opensky-network.org/auth/realms/opensky-network/protocol/openid-connect/token", strings.NewReader(data.Encode()))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := s.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("token request failed with status: %d", resp.StatusCode)
	}

	var tokenResp struct {
		AccessToken string `json:"access_token"`
		ExpiresIn   int    `json:"expires_in"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&tokenResp); err != nil {
		return err
	}

	s.token = tokenResp.AccessToken
	s.tokenExpiry = time.Now().Add(time.Duration(tokenResp.ExpiresIn) * time.Second)

	return nil
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
	start := time.Now()
	defer util.LogIfSlow(start, 5*time.Second, "OpenSkySeeder.Fetch")

	req, err := http.NewRequestWithContext(ctx, "GET", openSkyURL, nil)
	if err != nil {
		return nil, err
	}

	if s.authenticated {
		if err := s.getToken(ctx); err != nil {
			return nil, err
		}
		req.Header.Set("Authorization", "Bearer "+s.token)
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

		origin, destination := resolveRoute(callsign)

		entities = append(entities, entity.Entity{
			ID:          icao24,
			Source:      "opensky",
			Lat:         lat,
			Lng:         lng,
			Altitude:    altitude,
			Heading:     heading,
			Speed:       speed,
			CallSign:    callsign,
			Origin:      origin,
			Destination: destination,
			UpdatedAt:   time.Now().Unix(),
			Version:     0,
		})
	}

	return entities, nil
}
