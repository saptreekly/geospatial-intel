package seeder

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/saptreekly/OSINT/entity"
	"nhooyr.io/websocket"
	"nhooyr.io/websocket/wsjson"
)

const aisStreamURL = "wss://stream.aisstream.io/v0/stream"

// AISSeeder streams live maritime data from aisstream.io.
type AISSeeder struct {
	token    string
	interval time.Duration
}

// NewAISSeeder creates a new AIS seeder using AISSTREAM_TOKEN env var.
func NewAISSeeder() *AISSeeder {
	return &AISSeeder{
		token:    os.Getenv("AISSTREAM_TOKEN"),
		interval: 5 * time.Second,
	}
}

func (s *AISSeeder) Name() string {
	return "ais"
}

func (s *AISSeeder) Interval() time.Duration {
	return s.interval
}

// Fetch handles the connection and stream ingestion. 
// Note: As designed in seeder.Run(), Fetch() is called periodically.
// For a persistent stream, this implementation connects, reads, and returns 
// the latest batch of entities available within a short timeout.
func (s *AISSeeder) Fetch(ctx context.Context) ([]entity.Entity, error) {
	if s.token == "" {
		return nil, fmt.Errorf("AISSTREAM_TOKEN not set")
	}

	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	c, _, err := websocket.Dial(ctx, aisStreamURL, nil)
	if err != nil {
		return nil, err
	}
	defer c.Close(websocket.StatusInternalError, "closing")

	// Subscribe
	subRequest := map[string]interface{}{
		"APIKey": s.token,
		"BoundingBoxes": [][][]float64{
			{{-90, -180}, {90, 180}}, // Global
		},
	}
	if err := wsjson.Write(ctx, c, subRequest); err != nil {
		return nil, err
	}

	// Read a small batch
	var entities []entity.Entity
	for i := 0; i < 50; i++ {
		var msg map[string]interface{}
		if err := wsjson.Read(ctx, c, &msg); err != nil {
			break
		}

		// Simplified extraction based on AISStream format
		if msgType, ok := msg["MessageType"].(string); ok && msgType == "PositionReport" {
			data := msg["Message"].(map[string]interface{})["PositionReport"].(map[string]interface{})
			
			mmsi := fmt.Sprintf("%v", data["MMSI"])
			lat := data["Latitude"].(float64)
			lng := data["Longitude"].(float64)
			speed := data["Sog"].(float64)
			heading := data["Cog"].(float64) // Course Over Ground

			entities = append(entities, entity.Entity{
				ID:        mmsi,
				Source:    "ais",
				Lat:       lat,
				Lng:       lng,
				Heading:   heading,
				Speed:     speed,
				CallSign:  mmsi, // Default to MMSI if name not in this message
				UpdatedAt: time.Now().Unix(),
			})
		}
	}

	return entities, nil
}
