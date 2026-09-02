package audit

import (
	"testing"
	"time"

	"github.com/art-rustdesk/platform/art-api/internal/domain"
)

func TestNotificationProjection(t *testing.T) {
	now := time.Now().UTC()
	value, ok := notificationFor(domain.AuditEvent{Type: "device_identity_mismatch", TargetRustDeskID: "123456789", OccurredAt: now})
	if !ok || value.Severity != "critical" || value.Resource != "123456789" || value.CreatedAt != now || value.ID == "" {
		t.Fatalf("unexpected notification projection: %#v, ok=%v", value, ok)
	}
	if _, ok = notificationFor(domain.AuditEvent{Type: "login_success", OccurredAt: now}); ok {
		t.Fatal("routine successful login must not create an administrator notification")
	}
}
