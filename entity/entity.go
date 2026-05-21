package entity

import (
	"strconv"
	"strings"
)

type Entity struct {
	ID        string  `json:"id"`
	Source    string  `json:"source"`
	Lat       float64 `json:"lat"`
	Lng       float64 `json:"lng"`
	Altitude  float64 `json:"altitude"`
	Heading   float64 `json:"heading"`
	Speed     float64 `json:"speed"`
	CallSign  string  `json:"callSign"`
	Origin    string  `json:"origin,omitempty"`
	Destination string  `json:"destination,omitempty"`
	UpdatedAt int64   `json:"updatedAt"` // unix seconds
	Version   uint64  `json:"-"`         // internal monotonic counter
}

type Viewport struct {
	North, South, East, West float64
	Zoom                     int
}

func (vp *Viewport) IsGlobal() bool {
	return vp.North >= 90.0 && vp.South <= -90.0 && vp.East >= 180.0 && vp.West <= -180.0
}

type Cluster struct {
	Lat   float64 `json:"lat"`
	Lng   float64 `json:"lng"`
	Count int     `json:"count"`
}

type Delta struct {
	Seq      uint64             `json:"seq"`
	Added    []Entity           `json:"added,omitempty"`
	Updated  []Entity           `json:"updated,omitempty"`
	Removed  []string           `json:"removed,omitempty"`
	Clusters map[string]Cluster `json:"clusters,omitempty"` // H3 index string → Cluster
}

func (d *Delta) MarshalFast(sb *strings.Builder) {
	sb.WriteString(`{"seq":`)
	sb.WriteString(strconv.FormatUint(d.Seq, 10))

	// Encode Added array
	if len(d.Added) > 0 {
		sb.WriteString(`,"added":[`)
		for i, e := range d.Added {
			if i > 0 {
				sb.WriteString(",")
			}
			d.writeEntityFast(sb, &e)
		}
		sb.WriteString(`]`)
	}

	// Encode Updated array
	if len(d.Updated) > 0 {
		sb.WriteString(`,"updated":[`)
		for i, e := range d.Updated {
			if i > 0 {
				sb.WriteString(",")
			}
			d.writeEntityFast(sb, &e)
		}
		sb.WriteString(`]`)
	}

	// Encode Removed array
	if len(d.Removed) > 0 {
		sb.WriteString(`,"removed":[`)
		for i, id := range d.Removed {
			if i > 0 {
				sb.WriteString(",")
			}
			sb.WriteString(`"`)
			sb.WriteString(id)
			sb.WriteString(`"`)
		}
		sb.WriteString(`]`)
	}

	// Encode Clusters Map
	if len(d.Clusters) > 0 {
		sb.WriteString(`,"clusters":{`)
		first := true
		for k, c := range d.Clusters {
			if !first {
				sb.WriteString(",")
			}
			first = false
			sb.WriteString(`"`)
			sb.WriteString(k)
			sb.WriteString(`":{"lat":`)
			sb.WriteString(strconv.FormatFloat(c.Lat, 'f', 6, 64))
			sb.WriteString(`,"lng":`)
			sb.WriteString(strconv.FormatFloat(c.Lng, 'f', 6, 64))
			sb.WriteString(`,"count":`)
			sb.WriteString(strconv.Itoa(c.Count))
			sb.WriteString(`}`)
		}
		sb.WriteString(`}`)
	}
	sb.WriteString(`}`)
}

func (d *Delta) writeEntityFast(sb *strings.Builder, e *Entity) {
	sb.WriteString(`{"id":"`)
	sb.WriteString(e.ID)
	sb.WriteString(`","source":"`)
	sb.WriteString(e.Source)
	sb.WriteString(`","lat":`)
	sb.WriteString(strconv.FormatFloat(e.Lat, 'f', 6, 64))
	sb.WriteString(`,"lng":`)
	sb.WriteString(strconv.FormatFloat(e.Lng, 'f', 6, 64))
	sb.WriteString(`,"altitude":`)
	sb.WriteString(strconv.FormatFloat(e.Altitude, 'f', 1, 64))
	sb.WriteString(`,"heading":`)
	sb.WriteString(strconv.FormatFloat(e.Heading, 'f', 1, 64))
	sb.WriteString(`,"speed":`)
	sb.WriteString(strconv.FormatFloat(e.Speed, 'f', 1, 64))
	sb.WriteString(`,"callSign":"`)
	sb.WriteString(e.CallSign)
	sb.WriteString(`","origin":"`)
	sb.WriteString(e.Origin)
	sb.WriteString(`","destination":"`)
	sb.WriteString(e.Destination)
	sb.WriteString(`","updatedAt":`)
	sb.WriteString(strconv.FormatInt(e.UpdatedAt, 10))
	sb.WriteString(`}`)
	}
