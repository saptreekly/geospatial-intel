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
    <title>OSINT Aviation Tracker</title>
    <!-- MapLibre GL JS -->
    <script src="https://unpkg.com/maplibre-gl@3.6.2/dist/maplibre-gl.js"></script>
    <link href="https://unpkg.com/maplibre-gl@3.6.2/dist/maplibre-gl.css" rel="stylesheet" />
    <!-- Deck.gl -->
    <script src="https://unpkg.com/@deck.gl/core@8.9.0/dist.min.js"></script>
    <script src="https://unpkg.com/@deck.gl/layers@8.9.0/dist.min.js"></script>
    <style>
        body { margin: 0; padding: 0; overflow: hidden; background: #111; font-family: sans-serif; }
        #map { position: absolute; top: 0; bottom: 0; width: 100%; }
        #status {
            position: absolute; bottom: 20px; left: 20px; z-index: 10;
            background: rgba(0, 0, 0, 0.7); color: white;
            padding: 12px 16px; border-radius: 8px; font-size: 14px;
            backdrop-filter: blur(4px); border: 1px solid rgba(255,255,255,0.1);
        }
    </style>
</head>
<body>
    <div id="status">Initializing high-performance engine...</div>
    <div id="map"></div>
    <script>
        const markers = new Map();
        let deckgl;
        let ws;

        // Plane icon SVG path
        const PLANE_ICON = "M21,16V14L13,9V3.5A1.5,1.5 0 0,0 11.5,2A1.5,1.5 0 0,0 10,3.5V9L2,14V16L10,13.5V19L8,20.5V22L11.5,21L15,22V20.5L13,19V13.5L21,16Z";
        const ICON_SVG = '<svg xmlns="http://www.w3.org/2000/svg" width="24" height="24" viewBox="0 0 24 24"><path d="' + PLANE_ICON + '" fill="white"/></svg>';
        const ICON_DATA_URL = 'data:image/svg+xml;base64,' + btoa(ICON_SVG);

        function init() {
            deckgl = new deck.Deck({
                container: 'map',
                mapStyle: 'https://basemaps.cartocdn.com/gl/dark-matter-gl-style/style.json',
                initialViewState: {
                    longitude: 0,
                    latitude: 20,
                    zoom: 3,
                    pitch: 0,
                    bearing: 0
                },
                controller: true,
                onViewStateChange: ({viewState}) => {
                    deckgl.setProps({viewState});
                    sendViewport();
                },
                layers: []
            });
            connect();
        }

        function connect() {
            const proto = window.location.protocol === 'https:' ? 'wss:' : 'ws:';
            ws = new WebSocket(proto + '//' + window.location.host + '/stream');
            
            ws.onopen = () => {
                document.getElementById('status').innerText = 'Connected to OSINT Hub';
                sendViewport();
            };

            ws.onmessage = (evt) => {
                const delta = JSON.parse(evt.data);
                processDelta(delta);
            };

            ws.onerror = (err) => {
                console.error("WebSocket error:", err);
                document.getElementById('status').innerText = 'Connection error';
            };

            ws.onclose = () => {
                document.getElementById('status').innerText = 'Reconnecting...';
                setTimeout(connect, 2000);
            };
        }

        function sendViewport() {
            if (!ws || ws.readyState !== WebSocket.OPEN || !deckgl) return;
            
            const vs = deckgl.viewState || deckgl.props.initialViewState;
            const viewport = new deck.WebMercatorViewport({
                ...vs,
                width: deckgl.width || window.innerWidth,
                height: deckgl.height || window.innerHeight
            });
            
            const nw = viewport.unproject([0, 0]);
            const se = viewport.unproject([viewport.width, viewport.height]);

            ws.send(JSON.stringify({
                type: 'viewport',
                north: nw[1],
                south: se[1],
                east: se[0],
                west: nw[0],
                zoom: vs.zoom
            }));
        }

        function processDelta(delta) {
            // Update markers cache
            if (delta.added) delta.added.forEach(e => markers.set(e.id, e));
            if (delta.updated) delta.updated.forEach(e => markers.set(e.id, e));
            if (delta.removed) delta.removed.forEach(id => markers.delete(id));

            const aircraftData = Array.from(markers.values());
            
            const layer = new deck.IconLayer({
                id: 'aircraft-layer',
                data: aircraftData,
                pickable: true,
                iconAtlas: ICON_DATA_URL,
                iconMapping: {
                    airplane: {x: 0, y: 0, width: 24, height: 24, mask: true}
                },
                getIcon: d => 'airplane',
                getPosition: d => [d.lng, d.lat],
                getAngle: d => -d.heading,
                getColor: d => [255, 120, 0],
                getSize: d => 20,
                sizeScale: 1,
                transitions: {
                    getPosition: 600
                }
            });

            deckgl.setProps({ layers: [layer] });
            document.getElementById('status').innerText = aircraftData.length + " aircraft (60 FPS)";
        }

        init();
    </script>
</body>
</html>`
