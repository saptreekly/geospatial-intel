package entity

type Entity struct {
	ID        string  `json:"id"`
	Source    string  `json:"source"`
	Lat       float64 `json:"lat"`
	Lng       float64 `json:"lng"`
	Altitude  float64 `json:"altitude"`
	Heading   float64 `json:"heading"`
	Speed     float64 `json:"speed"`
	CallSign  string  `json:"callSign"`
	UpdatedAt int64   `json:"updatedAt"` // unix seconds
	Version   uint64  `json:"-"`         // internal monotonic counter
}

type Viewport struct {
	North, South, East, West float64
	Zoom                     int
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
