# Geospatial Real-Time Data Server

<!-- Badges -->
[![License: MIT](https://img.shields.io/badge/License-MIT-blue.svg)](https://opensource.org/licenses/MIT)
[![Go Version](https://img.shields.io/badge/Go-1.26+-blue.svg)](https://go.dev/)
[![Rust Version](https://img.shields.io/badge/Rust-1.80+-orange.svg)](https://www.rust-lang.org/)
[![Go Report Card](https://goreportcard.com/badge/github.com/jackweekly/OSINT)](https://goreportcard.com/report/github.com/jackweekly/OSINT)
![Performance](https://img.shields.io/badge/Query-O(1)%20lookup-green)

A high-performance Go server that streams real-time geospatial data (aircraft positions from OpenSky Network) to WebSocket clients, filtering by viewport and using H3 hexagonal binning for efficient spatial indexing.

## Architecture

Instead of broadcasting all 10,000+ entities to every client, this server:
- Accepts WebSocket connections with viewport declarations (lat/lng bounding box + zoom)
- Streams only entities visible in that viewport
- Groups out-of-view entities into H3 clusters with counts
- Pushes delta updates (added/updated/removed) rather than full snapshots each tick

### Key Components

- **Spatial Index** (`spatial/`): H3-based indexing with antimeridian-safe viewport queries
- **Entity Store** (`store/`): Thread-safe entity storage with stale eviction and pub/sub
- **Seeder** (`seeder/`): Pluggable data source interface; OpenSky implementation included
- **Server** (`server/`): HTTP + WebSocket server with per-client viewport filtering
- **Frontend** (embedded): Leaflet.js demo client (single HTML page)

## Building & Running

```bash
go run .
```

Server listens on `http://localhost:8080`. Open in a browser to see live aircraft on a map.

### Configuration

Environment variables:
- `PORT` — HTTP port (default: 8080)
- `MIN_PUSH_INTERVAL` — Min milliseconds between delta pushes per client (default: 500)
- `OPENSKY_USER` / `OPENSKY_PASS` — Optional OpenSky authentication (enables 5s poll instead of 10s)

### Example

```bash
PORT=9000 MIN_PUSH_INTERVAL=200 OPENSKY_USER=myuser OPENSKY_PASS=mypass go run .
```

## Protocol

### Client → Server (WebSocket)

```json
{
  "type": "viewport",
  "north": 40.8,
  "south": 40.0,
  "east": -73.8,
  "west": -74.2,
  "zoom": 6
}
```

### Server → Client (WebSocket)

```json
{
  "seq": 142,
  "added": [
    {
      "id": "abc123",
      "source": "opensky",
      "lat": 40.6,
      "lng": -74.1,
      "altitude": 10000,
      "heading": 270,
      "speed": 450,
      "callSign": "UAL582",
      "updatedAt": 1779233520
    }
  ],
  "updated": [...],
  "removed": ["def456"],
  "clusters": {
    "872830828ffffff": 43,
    "87283082bffffff": 12
  }
}
```

## H3 Zoom Resolution Mapping

| Zoom | H3 Resolution | Avg Cell Area |
|------|---------------|---------------|
| 0-4  | 2             | ~86,700 km²   |
| 5-7  | 4             | ~1,770 km²    |
| 8-10 | 6             | ~36 km²       |
| 11+  | 7             | ~5 km²        |

At low zooms, entities are clustered into larger cells. At high zooms, individual entities are shown.

## Design Notes

### Antimeridian Handling
When a viewport spans the International Date Line (west > east), the viewport query is split into two polygon queries and results are unioned. This prevents the h3.PolygonToCells function from returning garbage.

### OpenSky Parsing
Entity fields are extracted from OpenSky state vectors using gjson library (fast, type-safe JSON extraction by index).

### Rate Limiting
- Per-client delta pushes are rate-limited (default 500ms floor) to prevent flooding slow clients
- OpenSky seeder implements exponential backoff (max 60s) on 429/5xx errors
- Stale entities (absent from 2 consecutive polls) are marked removed to save bandwidth

### Delta Tracking
Per-client state maps entity ID → version. On each store event:
1. Query visible entities for client's viewport
2. Compare against client's seen versions
3. Emit added/updated/removed/clusters delta
4. Update client's seen versions

## Testing

```bash
# Run integration test
bash integration_test.sh

# Run server in foreground
go run .

# Test WebSocket in browser
# Open http://localhost:8080 and pan/zoom the map
# Check Network tab to verify delta messages (not full snapshots)
```

## Extending

### Adding a New Data Source

Implement the `seeder.Seeder` interface:

```go
type NewSeeder struct{}

func (s *NewSeeder) Name() string {
    return "mysource"
}

func (s *NewSeeder) Fetch(ctx context.Context) ([]entity.Entity, error) {
    // Fetch data from your API
    return entities, nil
}

func (s *NewSeeder) Interval() time.Duration {
    return 5 * time.Second
}
```

Then in `main.go`, start the seeder:

```go
seeder.Run(ctx, NewSeeder(), func(entities []entity.Entity) {
    s.Apply(entities)
})
```

## Performance Notes

- Spatial index is O(n) for queries (iterates all entities), suitable for <100k entities
- H3 cells at resolution 7 cover ~5 km² — at higher resolutions (8+), cell overhead increases
- WebSocket deltas are compressed by: (1) viewport filtering, (2) clustering out-of-view entities, (3) delta updates
- Per-client push rate-limiting prevents broadcast storms when many entities change simultaneously

For millions of entities, consider:
- Spatial partitioning (pre-index entities into geographic quadrants)
- Streaming cells instead of individual entities
- Client-side clustering with downloaded snapshots
