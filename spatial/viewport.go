package spatial

import (
	"github.com/jackweekly/osint/entity"
	"github.com/uber/h3-go/v4"
)

// ZoomToResolution maps viewport zoom level to H3 resolution.
func ZoomToResolution(zoom int) int {
	switch {
	case zoom < 5:
		return 2
	case zoom < 8:
		return 4
	case zoom < 11:
		return 6
	default:
		return 7
	}
}

// ViewportToCells converts a viewport to H3 cells at the appropriate resolution.
// Handles antimeridian wrapping (west > east) by splitting into two queries.
func ViewportToCells(vp entity.Viewport, resolution int) ([]h3.Cell, error) {
	// Special case for global viewport (covers entire Earth)
	if vp.North >= 90.0 && vp.South <= -90.0 && vp.East >= 180.0 && vp.West <= -180.0 {
		res0, err := h3.Res0Cells()
		if err != nil {
			return nil, err
		}
		return h3.UncompactCells(res0, resolution)
	}

	// Build polygon from bbox corners
	corners := h3.GeoLoop{
		h3.LatLng{Lat: vp.North, Lng: vp.West},
		h3.LatLng{Lat: vp.North, Lng: vp.East},
		h3.LatLng{Lat: vp.South, Lng: vp.East},
		h3.LatLng{Lat: vp.South, Lng: vp.West},
	}

	// If viewport crosses antimeridian (west > east), split into two queries
	if vp.West > vp.East {
		// Query 1: [west, 180]
		corners1 := h3.GeoLoop{
			h3.LatLng{Lat: vp.North, Lng: vp.West},
			h3.LatLng{Lat: vp.North, Lng: 180},
			h3.LatLng{Lat: vp.South, Lng: 180},
			h3.LatLng{Lat: vp.South, Lng: vp.West},
		}
		polygon1 := h3.GeoPolygon{GeoLoop: corners1}
		cells1, err := polygon1.Cells(resolution)
		if err != nil {
			return nil, err
		}

		// Query 2: [-180, east]
		corners2 := h3.GeoLoop{
			h3.LatLng{Lat: vp.North, Lng: -180},
			h3.LatLng{Lat: vp.North, Lng: vp.East},
			h3.LatLng{Lat: vp.South, Lng: vp.East},
			h3.LatLng{Lat: vp.South, Lng: -180},
		}
		polygon2 := h3.GeoPolygon{GeoLoop: corners2}
		cells2, err := polygon2.Cells(resolution)
		if err != nil {
			return nil, err
		}

		// Union results
		cellMap := make(map[h3.Cell]struct{})
		for _, c := range cells1 {
			cellMap[c] = struct{}{}
		}
		for _, c := range cells2 {
			cellMap[c] = struct{}{}
		}
		cells := make([]h3.Cell, 0, len(cellMap))
		for c := range cellMap {
			cells = append(cells, c)
		}
		return cells, nil
	}

	// Normal case: simple polygon query
	polygon := h3.GeoPolygon{GeoLoop: corners}
	return polygon.Cells(resolution)
}
