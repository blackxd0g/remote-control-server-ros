package relaygateway

import "time"

type QuarantineEvent struct {
	ID        string `json:"id"`
	UUID      string `json:"uuid"`
	Status    string `json:"status"`
	CreatedAt uint64 `json:"created_at"`
	Reason    string `json:"reason"`
}
type DeliveryStatus struct {
	Pending     int               `json:"pending"`
	Quarantined int               `json:"quarantined"`
	Reserved    int               `json:"reserved"`
	Capacity    int               `json:"capacity"`
	Healthy     bool              `json:"healthy"`
	Blocked     bool              `json:"blocked"`
	OldestAt    uint64            `json:"oldest_at"`
	Sample      []QuarantineEvent `json:"sample"`
}

func (v *DeliveryStatus) valid() bool {
	if v == nil {
		return true
	}
	if v.Capacity != 4096 || v.Pending < 0 || v.Pending > 4096 || v.Quarantined < 0 || v.Quarantined > 4096 || v.Reserved < 0 || v.Reserved > 4096 || v.Pending+v.Quarantined+v.Reserved > 4096 || len(v.Sample) > 5 || len(v.Sample) > v.Quarantined || v.OldestAt > uint64(time.Now().Add(5*time.Minute).UnixMilli()) {
		return false
	}
	for _, e := range v.Sample {
		if len(e.ID) > 64 || len(e.ID) == 0 || len(e.UUID) > 128 || len(e.UUID) < 8 || (e.Status != "active" && e.Status != "closed") || e.Reason != "credential_rotated" || e.CreatedAt > uint64(time.Now().Add(5*time.Minute).UnixMilli()) {
			return false
		}
	}
	return true
}

type DeliveryCounts struct {
	Authorizations int `json:"authorizations"`
	Receipts       int `json:"receipts"`
	Archived       int `json:"archived"`
	Eligible       int `json:"eligible"`
}
type Observation struct {
	Connected  bool            `json:"connected"`
	ObservedAt time.Time       `json:"observed_at"`
	Delivery   *DeliveryStatus `json:"delivery"`
}

func (g *Gateway) DeliveryObservation(id string) Observation {
	g.mu.Lock()
	defer g.mu.Unlock()
	p := g.peers[id]
	if p == nil {
		return Observation{}
	}
	return Observation{Connected: p.ready, ObservedAt: p.observedAt, Delivery: p.delivery}
}
