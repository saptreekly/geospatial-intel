package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/saptreekly/geospatial-intel/store"
)

// Server is the HTTP server for the geospatial data service.
type Server struct {
	http.Server
	hub             *Hub
	minPushInterval time.Duration
}

// NewServer creates a new HTTP server.
func NewServer(addr string, s *store.Store, minPushInterval time.Duration) *Server {
	hub := NewHub(s)
	srv := &Server{
		Server: http.Server{
			Addr:         addr,
			ReadTimeout:  15 * time.Second,
			WriteTimeout: 15 * time.Second,
		},
		hub:             hub,
		minPushInterval: minPushInterval,
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/" {
			w.Header().Set("Content-Type", "text/html")
			w.Write([]byte(indexHTML))
			return
		}
		http.NotFound(w, r)
	})

	mux.HandleFunc("/stream", func(w http.ResponseWriter, r *http.Request) {
		StreamHandler(context.Background(), w, r, hub, minPushInterval)
	})

	mux.HandleFunc("/api/history", func(w http.ResponseWriter, r *http.Request) {
		id := r.URL.Query().Get("id")
		if id == "" {
			http.Error(w, "missing id", http.StatusBadRequest)
			return
		}
		history, err := s.GetHistory(id)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(history)
	})

	srv.Handler = mux
	return srv
}

// Start starts the HTTP server.
func (s *Server) Start() error {
	fmt.Printf("Server listening on http://%s\n", s.Addr)
	return s.ListenAndServe()
}

// indexHTML is the high-performance Deck.gl + Maplibre GL client.
var indexHTML = `<!DOCTYPE html>
<html>
<head>
    <title>Tactical OSINT Hub</title>
    <!-- MapLibre GL JS -->
    <script src="https://unpkg.com/maplibre-gl@3.6.2/dist/maplibre-gl.js"></script>
    <link href="https://unpkg.com/maplibre-gl@3.6.2/dist/maplibre-gl.css" rel="stylesheet" />
    <!-- Deck.gl -->
    <script src="https://unpkg.com/@deck.gl/core@8.9.0/dist.min.js"></script>
    <script src="https://unpkg.com/@deck.gl/layers@8.9.0/dist.min.js"></script>
    <style>
        body { margin: 0; padding: 0; overflow: hidden; background: #111; font-family: 'Courier New', monospace; }
        #map { position: absolute; top: 0; bottom: 0; width: 100%; }
        #status {
            position: absolute; bottom: 20px; left: 20px; z-index: 10;
            background: rgba(0, 20, 0, 0.85); color: #00ff78;
            padding: 12px 16px; border-radius: 4px; font-size: 13px;
            backdrop-filter: blur(4px); border: 1px solid rgba(0,255,120,0.3);
            box-shadow: 0 0 15px rgba(0,255,120,0.1);
        }
    </style>
</head>
<body>
    <div id="status">INITIATING TACTICAL UPLINK...</div>
    <div id="map"></div>
    <script>
        const markers = new Map();
        let map;
        let deckOverlay;
        let ws;

        function init() {
            // 1. Initialize MapLibre GL first
            map = new maplibregl.Map({
                container: 'map',
                style: 'https://basemaps.cartocdn.com/gl/dark-matter-gl-style/style.json',
                center: [0, 20],
                zoom: 3,
                interactive: true
            });

            // 2. Create the Deck.gl Overlay
            deckOverlay = new deck.MapboxOverlay({
                interleaved: false,
                layers: []
            });

            // 3. Add Deck as a control to MapLibre
            map.addControl(deckOverlay);

            // 4. Bind view state sync
            map.on('moveend', sendViewport);
            map.on('zoomend', sendViewport);

            connect();
        }

        function sendViewport() {
            if (!ws || ws.readyState !== WebSocket.OPEN || !map) return;
            const bounds = map.getBounds();

            // Clamp boundaries to strict physical global maximums
            const north = Math.min(89.9, bounds.getNorth());
            const south = Math.max(-89.9, bounds.getSouth());

            let east = bounds.getEast();
            let west = bounds.getWest();

            // Handle MapLibre map wrapping/infinite panning extensions
            if (east - west >= 360) {
                east = 180;
                west = -180;
            } else {
                // Normalize longitudes to stay within [-180, 180]
                east = ((east + 180) % 360 + 360) % 360 - 180;
                west = ((west + 180) % 360 + 360) % 360 - 180;
            }

            ws.send(JSON.stringify({
                type: 'viewport',
                north: north,
                south: south,
                east: east,
                west: west,
                zoom: Math.round(map.getZoom())
            }));
        }

        function processDelta(delta) {
            if (delta.added) delta.added.forEach(e => markers.set(e.id, e));
            if (delta.updated) delta.updated.forEach(e => markers.set(e.id, e));
            if (delta.removed) delta.removed.forEach(id => markers.delete(id));

            const aircraftData = Array.from(markers.values());

            // 5. Use high-performance Scatterplot returns
            const layer = new deck.ScatterplotLayer({
                id: 'aircraft-layer',
                data: aircraftData,
                getPosition: d => [d.lng, d.lat],
                getRadius: d => 15,
                radiusScale: 1000,
                radiusMinPixels: 4,
                radiusMaxPixels: 15,
                getFillColor: d => [0, 255, 120, 200], // Luminous Tactical Green
                pickable: true,
                updateTriggers: {
                    getPosition: [delta.seq]
                }
            });

            deckOverlay.setProps({ layers: [layer] });
            document.getElementById('status').innerText = aircraftData.length + " TARGETS TRACKING (60 FPS)";
        }

        function connect() {
            const proto = window.location.protocol === 'https:' ? 'wss:' : 'ws:';
            ws = new WebSocket(proto + '//' + window.location.host + '/stream');
            ws.onopen = () => {
                document.getElementById('status').innerText = 'CONNECTED TO TACTICAL OSINT HUB';
                sendViewport();
            };
            ws.onmessage = (evt) => {
                processDelta(JSON.parse(evt.data));
            };
            ws.onclose = () => {
                document.getElementById('status').innerText = 'RECONNECTING...';
                setTimeout(connect, 2000);
            };
        }

        init();
    </script>
</body>
</html>`
