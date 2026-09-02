package relay

import (
	"testing"

	"github.com/art-rustdesk/platform/art-api/internal/domain"
)

func TestSelectPrefersLoadThenLatency(t *testing.T) {
	values := []domain.RelayServer{
		{ID: "offline", Enabled: true, Health: "offline", Region: "msk", LatencyMS: 1},
		{ID: "busy", Enabled: true, Health: "healthy", Region: "msk", Connections: 10, LatencyMS: 1},
		{ID: "fast", Enabled: true, Health: "healthy", Region: "msk", Connections: 2, LatencyMS: 20},
		{ID: "slow", Enabled: true, Health: "healthy", Region: "msk", Connections: 2, LatencyMS: 80},
	}
	selected, err := Select(values, "msk")
	if err != nil || selected.ID != "fast" {
		t.Fatalf("selected=%q err=%v", selected.ID, err)
	}
}

func TestSelectDoesNotCrossRequestedRegion(t *testing.T) {
	_, err := Select([]domain.RelayServer{{ID: "eu", Enabled: true, Health: "healthy", Region: "eu"}}, "msk")
	if err != domain.ErrNotFound {
		t.Fatalf("expected not found, got %v", err)
	}
}
