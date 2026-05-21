package server

import (
	"context"
	"encoding/json"
	"fmt"
	"mime"
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
	mime.AddExtensionType(".wasm", "application/wasm")
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

	fs := http.FileServer(http.Dir("./static/wasm"))
	mux.Handle("/wasm/", http.StripPrefix("/wasm/", fs))

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

// indexHTML is the high-performance Maplibre GL client.
var indexHTML = `<!DOCTYPE html>
<html>
<head>
    <title>Tactical OSINT Hub</title>
    <!-- MapLibre GL JS -->
    <script src="https://unpkg.com/maplibre-gl@3.6.2/dist/maplibre-gl.js"></script>
    <link href="https://unpkg.com/maplibre-gl@3.6.2/dist/maplibre-gl.css" rel="stylesheet" />
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
        #side-panel {
            position: absolute; top: 0; right: -320px; width: 300px; bottom: 0; z-index: 15;
            background: rgba(0, 15, 5, 0.95); color: #00ff78; padding: 20px;
            border-left: 1px solid rgba(0, 255, 120, 0.3); box-shadow: -5px 0 25px rgba(0,0,0,0.5);
            transition: right 0.3s ease; backdrop-filter: blur(6px);
        }
        #side-panel.open { right: 0; }
        .close-btn { float: right; cursor: pointer; font-weight: bold; padding: 0 5px; }
        .panel-row { margin: 15px 0; border-bottom: 1px solid rgba(0,255,120,0.1); padding-bottom: 5px; }
    </style>
</head>
<body>
    <div id="status">INITIATING TACTICAL UPLINK...</div>
    <div id="map"></div>
    <div id="side-panel">
        <span class="close-btn" onclick="closePanel()">&times;</span>
        <h3>TARGET DATA</h3>
        <div id="panel-content">Select a target for interception telemetry.</div>
    </div>
    <script type="module">
        import initWasm, { RadarEngine } from '/wasm/frontend_wasm.js';

        let radarEngine;
        let wasm;
        const persistentGeoJSON = { type: 'FeatureCollection', features: [] };
        const markers = new Map();
        let map;
        let ws;

        const PLANE_SVG = '<svg xmlns="http://www.w3.org/2000/svg" width="32" height="32" viewBox="0 0 24 24"><path d="M21,16V14L13,9V3.5A1.5,1.5 0 0,0 11.5,2A1.5,1.5 0 0,0 10,3.5V9L2,14V16L10,13.5V19L8,20.5V22L11.5,21L15,22V20.5L13,19V13.5L21,16Z" fill="#00ff78" stroke="#111" stroke-width="0.5"/></svg>';
        const UPDATE_INTERVAL = 25000; // 25 second polling window matching backend seeder

        async function init() {
            wasm = await initWasm();
            radarEngine = new RadarEngine();

            // 1. Initialize the native hardware-accelerated map
            map = new maplibregl.Map({
                container: 'map',
                style: 'https://basemaps.cartocdn.com/gl/dark-matter-gl-style/style.json',
                center: [0, 20],
                zoom: 3,
                interactive: true
            });

            // 2. Once WebGL context is ready, inject the data layers
            map.on('load', () => {
                // Convert SVG to browser image texture
                const blob = new Blob([PLANE_SVG], {type: 'image/svg+xml'});
                const url = URL.createObjectURL(blob);
                const img = new Image();

                img.onload = () => {
                    map.addImage('plane-icon', img);

                    map.addSource('aircraft', {
                        type: 'geojson',
                        data: { type: 'FeatureCollection', features: [] }
                    });

                    // Add history source & layer
                    map.addSource('aircraft-history', {
                        type: 'geojson',
                        data: { type: 'FeatureCollection', features: [] }
                    });
                    map.addLayer({
                        id: 'history-layer',
                        type: 'line',
                        source: 'aircraft-history',
                        layout: { 'line-join': 'round', 'line-cap': 'round' },
                        paint: { 'line-color': '#00ff78', 'line-width': 2, 'line-dasharray': [2, 2] }
                    });

                    map.addLayer({
                        id: 'aircraft-layer',
                        type: 'symbol',
                        source: 'aircraft',
                        layout: {
                            'icon-image': 'plane-icon',
                            'icon-size': 0.8,
                            'icon-rotate': ['get', 'heading'],
                            'icon-rotation-alignment': 'map',
                            'icon-allow-overlap': true,
                            'icon-ignore-placement': true
                        }
                    });

                    // Interaction Handlers
                    map.on('click', 'aircraft-layer', (e) => {
                        const f = e.features[0];
                        if (f) showTargetTelemetry(f.properties.id);
                    });
                    map.on('mouseenter', 'aircraft-layer', () => map.getCanvas().style.cursor = 'pointer');
                    map.on('mouseleave', 'aircraft-layer', () => map.getCanvas().style.cursor = '');

                    connect();
                    animatePlanes();
                };
                img.src = url;
            });

            map.on('moveend', sendViewport);
            map.on('zoomend', sendViewport);
        }

        function animatePlanes() {
            if (!map || !map.getSource('aircraft') || !radarEngine || !wasm) {
                requestAnimationFrame(animatePlanes);
                return;
            }

            const now = performance.now();
            radarEngine.tick(now, UPDATE_INTERVAL);

            const count = radarEngine.get_total_count();
            const ptr = radarEngine.get_data_ptr();
            // Access WASM memory directly without allocations
            const data = new Float64Array(wasm.memory.buffer, ptr, count * 4);

            // Synchronize persistent GeoJSON features with raw data
            if (persistentGeoJSON.features.length !== count) {
                persistentGeoJSON.features = new Array(count);
                for (let i = 0; i < count; i++) {
                    persistentGeoJSON.features[i] = {
                        type: 'Feature',
                        geometry: { type: 'Point', coordinates: [0, 0] },
                        properties: { id: '', heading: 0 }
                    };
                }
            }

            for (let i = 0; i < count; i++) {
                const offset = i * 4;
                const feat = persistentGeoJSON.features[i];
                feat.geometry.coordinates[0] = data[offset];     // lng
                feat.geometry.coordinates[1] = data[offset + 1]; // lat
                feat.properties.heading = data[offset + 2];      // heading
            }

            map.getSource('aircraft').setData(persistentGeoJSON);
            document.getElementById('status').innerText = count + " TARGETS TRACKING (WASM-ZERO-COPY)";
            requestAnimationFrame(animatePlanes);
        }

        function showTargetTelemetry(id) {
            const p = markers.get(id);
            if (!p) return;

            // 1. Instantly populate side panel metadata from active memory cache
            document.getElementById('side-panel').classList.add('open');
            document.getElementById('panel-content').innerHTML = 
                '<div class="panel-row"><b>ICAO24:</b> ' + p.id + '</div>' +
                '<div class="panel-row"><b>CALLSIGN:</b> ' + (p.callSign || 'UNKNOWN') + '</div>' +
                '<div class="panel-row"><b>SOURCE:</b> ' + p.source.toUpperCase() + '</div>' +
                '<div class="panel-row"><b>SPEED:</b> ' + Math.round(p.speed) + ' kn</div>' +
                '<div class="panel-row"><b>ALTITUDE:</b> ' + Math.round(p.altitude) + ' ft</div>' +
                '<div class="panel-row"><b>HEADING:</b> ' + Math.round(p.heading) + '°</div>' +
                '<div class="panel-row"><b>LAT/LNG:</b> ' + p.lat.toFixed(4) + ', ' + p.lng.toFixed(4) + '</div>';

            // 2. Query Go server history API to dynamically map historical data path vectors
            fetch('/api/history?id=' + encodeURIComponent(id))
                .then(res => res.json())
                .then(historyData => {
                    if (!historyData || historyData.length < 2) return;

                    // Sort ascending (oldest logs first)
                    const sortedHistory = [...historyData].sort((a, b) => a.updatedAt - b.updatedAt);

                    // Isolate ONLY the most recent continuous flight leg by walking backward from the latest point
                    const activeCoordinates = [];
                    const lastPoint = sortedHistory[sortedHistory.length - 1];
                    activeCoordinates.push([lastPoint.lng, lastPoint.lat]);

                    for (let i = sortedHistory.length - 2; i >= 0; i--) {
                        const gapSeconds = sortedHistory[i+1].updatedAt - sortedHistory[i].updatedAt;

                        // Break immediately if a gap wider than 15 minutes is found (indicates a past flight leg)
                        if (gapSeconds > 900) {
                            break;
                        }
                        activeCoordinates.push([sortedHistory[i].lng, sortedHistory[i].lat]);
                    }

                    // Restore chronological order for MapLibre LineString rendering
                    activeCoordinates.reverse();

                    if (activeCoordinates.length >= 2) {
                        map.getSource('aircraft-history').setData({
                            type: 'FeatureCollection',
                            features: [{
                                type: 'Feature',
                                geometry: { type: 'LineString', coordinates: activeCoordinates }
                            }]
                        });
                    } else {
                        map.getSource('aircraft-history').setData({ type: 'FeatureCollection', features: [] });
                    }
                })
                .catch(err => console.error("Error loading trail telemetry:", err));
        }

        window.closePanel = function() {
            document.getElementById('side-panel').classList.remove('open');
            map.getSource('aircraft-history').setData({ type: 'FeatureCollection', features: [] });
        }

        function sendViewport() {
            if (!ws || ws.readyState !== WebSocket.OPEN || !map) {
                return;
            }
            const bounds = map.getBounds();
            const north = Math.min(89.9, bounds.getNorth());
            const south = Math.max(-89.9, bounds.getSouth());
            let east = bounds.getEast();
            let west = bounds.getWest();

            if (east - west >= 360) { east = 180; west = -180; }
            else {
                east = ((east + 180) % 360 + 360) % 360 - 180;
                west = ((west + 180) % 360 + 360) % 360 - 180;
            }

            ws.send(JSON.stringify({
                type: 'viewport',
                north: north, south: south, east: east, west: west,
                zoom: Math.round(map.getZoom())
            }));
        }

        function processDelta(delta) {
            const now = performance.now();
            console.log("Delta received:", { added: delta.added?.length, updated: delta.updated?.length, removed: delta.removed?.length });

            // Sync standard cache for local sidebar details panel queries
            if (delta.added) delta.added.forEach(e => markers.set(e.id, e));
            if (delta.updated) delta.updated.forEach(e => markers.set(e.id, e));
            if (delta.removed) delta.removed.forEach(id => markers.delete(id));

            // Pipe updates directly into Rust's linear allocator memory heap
            if (radarEngine) {
                radarEngine.update_targets(delta.added || [], delta.updated || [], delta.removed || [], now);
            }
        }

        function connect() {
            const proto = window.location.protocol === 'https:' ? 'wss:' : 'ws:';
            ws = new WebSocket(proto + '//' + window.location.host + '/stream');
            ws.onopen = () => {
                document.getElementById('status').innerText = 'CONNECTED TO TACTICAL OSINT HUB';
                sendViewport();
            };
            ws.onmessage = (evt) => { processDelta(JSON.parse(evt.data)); };
            ws.onclose = () => {
                document.getElementById('status').innerText = 'RECONNECTING...';
                setTimeout(connect, 2000);
            };
        }

        init();
    </script>
</body>
</html>`
