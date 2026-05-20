package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/saptreekly/OSINT/store"
)

// Server is the HTTP server for the geospatial data service.
type Server struct {
	http.Server
	hub               *Hub
	minPushInterval   time.Duration
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

// indexHTML is the demo Leaflet.js client (defined at the end of this file).
var indexHTML = `<!DOCTYPE html>
<html>
<head>
    <title>Geospatial Server</title>
    <link rel="stylesheet" href="https://unpkg.com/leaflet@1.9.4/dist/leaflet.css" />
    <style>
        * { margin: 0; padding: 0; }
        body { font-family: sans-serif; }
        #map { position: absolute; top: 0; left: 0; width: 100%; height: 100%; }
        #status { position: absolute; bottom: 10px; left: 10px; background: rgba(0,0,0,0.7);
                  color: #fff; padding: 10px; border-radius: 4px; font-size: 12px; }
        .marker-cluster { background: #51aada; border-radius: 50%; color: white;
                         text-align: center; display: flex; align-items: center; justify-content: center;
                         font-weight: bold; font-size: 14px; }
    </style>
</head>
<body>
    <div id="map"></div>
    <div id="status">Loading...</div>

    <script src="https://unpkg.com/leaflet@1.9.4/dist/leaflet.js"></script>
    <script src="https://unpkg.com/leaflet-marker-slideto@0.2.0/Leaflet.Marker.SlideTo.js"></script>
    <script>
        const map = L.map('map').setView([20, 0], 3);
        L.tileLayer('https://{s}.tile.openstreetmap.org/{z}/{x}/{y}.png', {
            attribution: '&copy; OpenStreetMap contributors',
            maxZoom: 19
        }).addTo(map);

        const markers = {}; // entityID → marker
        const clusterMarkers = {}; // cellIndex → cluster marker
        let ws = null;
        let entityCount = 0;
        let clusterCount = 0;

        function connect() {
            const proto = window.location.protocol === 'https:' ? 'wss:' : 'ws:';
            ws = new WebSocket(proto + '//' + window.location.host + '/stream');
            ws.onopen = () => {
                document.getElementById('status').textContent = 'Connected';
                sendViewport();
            };
            ws.onmessage = (e) => {
                const delta = JSON.parse(e.data);
                processDelta(delta);
            };
            ws.onerror = (e) => {
                document.getElementById('status').textContent = 'Error: ' + e;
            };
            ws.onclose = () => {
                document.getElementById('status').textContent = 'Disconnected. Reconnecting...';
                setTimeout(connect, 2000);
            };
        }

        function sendViewport() {
            if (ws && ws.readyState === WebSocket.OPEN) {
                const bounds = map.getBounds();
                ws.send(JSON.stringify({
                    type: 'viewport',
                    north: bounds.getNorth(),
                    south: bounds.getSouth(),
                    east: bounds.getEast(),
                    west: bounds.getWest(),
                    zoom: map.getZoom()
                }));
            }
        }

        function processDelta(delta) {
            // Add new entities
            for (const e of delta.added || []) {
                // A simple clean SVG path pointing north (0 degrees)
                const planeSVG = '<svg viewBox="0 0 24 24" width="24" height="24" style="transform: rotate(' + e.heading + 'deg); transform-origin: center;">' +
                    '<path fill="#ff7800" stroke="#000" stroke-width="1" d="M21,16V14L13,9V3.5A1.5,1.5 0 0,0 11.5,2A1.5,1.5 0 0,0 10,3.5V9L2,14V16L10,13.5V19L8,20.5V22L11.5,21L15,22V20.5L13,19V13.5L21,16Z"/>' +
                '</svg>';

                const marker = L.marker([e.lat, e.lng], {
                    icon: L.divIcon({
                        html: planeSVG,
                        className: 'plane-marker',
                        iconSize: [24, 24],
                        iconAnchor: [12, 12]
                    })
                }).bindPopup('<b>' + (e.callSign || 'UNKNOWN') + '</b><br>Speed: ' + e.speed + 'kn<br>Alt: ' + e.altitude + 'ft').addTo(map);
                markers[e.id] = marker;
            }

            // Update existing entities
            for (const e of delta.updated || []) {
                if (markers[e.id]) {
                    // Update position smoothly over 2 seconds
                    markers[e.id].slideTo([e.lat, e.lng], {
                        duration: 2000,
                        keepAtCenter: false
                    });
                }
            }

            // Remove entities no longer in view
            for (const id of delta.removed || []) {
                if (markers[id]) {
                    map.removeLayer(markers[id]);
                    delete markers[id];
                }
            }

            // Update cluster markers
            for (const id of Object.keys(clusterMarkers)) {
                map.removeLayer(clusterMarkers[id]);
                delete(clusterMarkers[id]);
            }
            for (const [cellIdx, cluster] of Object.entries(delta.clusters || {})) {
                // Use a divIcon to show the count directly on the map
                const clusterIcon = L.divIcon({
                    className: 'marker-cluster',
                    html: '<div><span>' + cluster.count + '</span></div>',
                    iconSize: [30, 30],
                    iconAnchor: [15, 15]
                });
                
                const clusterMarker = L.marker([cluster.lat, cluster.lng], {
                    icon: clusterIcon
                }).bindPopup("Cluster: " + cluster.count + " aircraft").addTo(map);

                clusterMarkers[cellIdx] = clusterMarker;
            }


            entityCount = Object.keys(markers).length;
            clusterCount = Object.keys(clusterMarkers).length;
            updateStatus();
        }

        function updateStatus() {
            document.getElementById('status').textContent =
                entityCount + " aircraft | " + clusterCount + " clusters";
        }

        map.on('moveend', sendViewport);
        map.on('zoomend', sendViewport);

        connect();
    </script>
</body>
</html>`

