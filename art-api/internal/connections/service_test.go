package connections_test

import (
	"testing"
	"time"

	"github.com/art-rustdesk/platform/art-api/internal/connections"
	"github.com/art-rustdesk/platform/art-api/internal/domain"
)

func TestBuildTracksActiveAndClosedConnections(t *testing.T) {
	now := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
	metadata := map[string]any{"connection_id": float64(7), "session_id": "12", "connection_type": float64(1), "controller_display_name": "Operator"}
	events := []domain.AuditEvent{
		{Type: "connection_closed", OccurredAt: now.Add(-time.Minute), TargetRustDeskID: "200", ControllerDevice: "100", Metadata: metadata},
		{Type: "connection_started", OccurredAt: now.Add(-6 * time.Minute), TargetRustDeskID: "200", ControllerDevice: "100", ActorUserID: "user-1", Metadata: metadata},
		{Type: "connection_started", OccurredAt: now.Add(-2 * time.Minute), TargetRustDeskID: "300", ControllerDevice: "100", Metadata: map[string]any{"connection_id": float64(8)}},
	}
	result := connections.Build(events, now)
	if result.Active != 1 || result.Closed != 1 || len(result.Items) != 2 {
		t.Fatalf("unexpected snapshot: %#v", result)
	}
	var closed *connections.Record
	for index := range result.Items {
		if result.Items[index].Status == "closed" {
			closed = &result.Items[index]
		}
	}
	if closed == nil || closed.DurationSeconds != 300 || closed.ActorUserID != "user-1" {
		t.Fatalf("unexpected closed record: %#v", closed)
	}
}

func TestBuildUsesChronologyInsteadOfInputOrder(t *testing.T) {
	now := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
	metadata := map[string]any{"connection_id": float64(9), "session_id": "4"}
	events := []domain.AuditEvent{
		{Type: "connection_closed", OccurredAt: now.Add(-time.Minute), TargetRustDeskID: "200", ControllerDevice: "100", Metadata: metadata},
		{Type: "connection_started", OccurredAt: now.Add(-5 * time.Minute), TargetRustDeskID: "200", ControllerDevice: "100", Metadata: metadata},
		{Type: "connection_updated", OccurredAt: now.Add(-3 * time.Minute), TargetRustDeskID: "200", ControllerDevice: "100", Metadata: metadata},
	}
	result := connections.Build(events, now)
	if result.Closed != 1 || result.Active != 0 || result.Items[0].DurationSeconds != 240 {
		t.Fatalf("unexpected chronological snapshot: %#v", result)
	}
}

func TestBuildRecordsMarksStalePersistentConnection(t *testing.T) {
	now := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
	result := connections.BuildRecords([]domain.ConnectionRecord{{Key: "key", TargetRustDeskID: "200", StartedAt: now.Add(-time.Hour), LastSeenAt: now.Add(-11 * time.Minute)}}, now)
	if result.Stale != 1 || result.Active != 0 || result.Items[0].Status != "stale" {
		t.Fatalf("unexpected snapshot: %#v", result)
	}
}
