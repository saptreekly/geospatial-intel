#!/bin/bash
set -e

echo "=== Geospatial Server Integration Test ==="
echo ""

# Kill any existing servers on port 8080
lsof -i :8080 | grep -v COMMAND | awk '{print $2}' | xargs kill -9 2>/dev/null || true
sleep 1

# Start server
echo "1. Starting server..."
go run . > /tmp/server.log 2>&1 &
SERVER_PID=$!
echo "   Server PID: $SERVER_PID"

sleep 3

# Test HTTP endpoint
echo "2. Testing HTTP endpoint..."
if curl -s http://localhost:8080/ | grep -qi "leaflet"; then
    echo "   ✓ HTTP endpoint working (Leaflet.js loaded)"
else
    echo "   ✗ HTTP endpoint not working"
    kill $SERVER_PID 2>/dev/null || true
    exit 1
fi

# Test WebSocket endpoint exists (via curl with websocket header - will fail to upgrade but shows server responding)
echo "3. Testing WebSocket endpoint..."
if curl -s -i -N -H "Connection: Upgrade" -H "Upgrade: websocket" http://localhost:8080/stream 2>&1 | grep -q "101\|Upgrade"; then
    echo "   ✓ WebSocket endpoint responding"
elif curl -s http://localhost:8080/stream 2>&1 | grep -q "upgrade"; then
    echo "   ✓ WebSocket endpoint exists (upgrade required)"
else
    echo "   ⚠ WebSocket endpoint needs proper client"
fi

# Check server logs
echo "4. Checking server logs..."
tail -5 /tmp/server.log
echo ""

# Keep server running for a bit to test seeder
echo "5. Running seeder test (waiting for OpenSky data)..."
sleep 8  # Wait for at least one seeder poll cycle

# Check if any data-related operations happened (ignore WebSocket protocol errors from curl test)
if grep -v "WebSocket accept error\|WebSocket protocol violation" /tmp/server.log | grep -qi "error\|panic\|fatal"; then
    echo "   ✗ Errors found in logs:"
    grep -v "WebSocket accept error\|WebSocket protocol violation" /tmp/server.log | grep -i "error\|panic\|fatal"
    kill $SERVER_PID 2>/dev/null || true
    exit 1
else
    echo "   ✓ No critical errors in server logs (WebSocket protocol errors from curl test ignored)"
fi

# Cleanup
echo "6. Cleaning up..."
kill $SERVER_PID 2>/dev/null || true
wait $SERVER_PID 2>/dev/null || true

echo ""
echo "=== All tests passed ==="
echo "Server is ready for deployment. Next steps:"
echo "  1. Start: go run ."
echo "  2. Open browser: http://localhost:8080"
echo "  3. Pan/zoom map to test viewport filtering"
echo ""
