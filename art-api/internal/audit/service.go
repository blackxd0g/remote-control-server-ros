package audit

import (
	"context"
	"time"

	"github.com/art-rustdesk/platform/art-api/internal/domain"
	"github.com/art-rustdesk/platform/art-api/internal/events"
	"github.com/google/uuid"
)

type Service struct {
	repository domain.Repository
	hub        *events.Hub
}

func New(repository domain.Repository) *Service { return &Service{repository: repository} }
func NewWithHub(repository domain.Repository, hub *events.Hub) *Service {
	return &Service{repository: repository, hub: hub}
}

func (s *Service) Record(ctx context.Context, event domain.AuditEvent) error {
	if event.ID == "" {
		event.ID = uuid.NewString()
	}
	if event.OccurredAt.IsZero() {
		event.OccurredAt = time.Now().UTC()
	}
	if err := s.repository.AppendAudit(ctx, event); err != nil {
		return err
	}
	if notification, ok := notificationFor(event); ok {
		// Notifications are a convenience projection. The immutable audit write
		// above must remain successful even if this projection cannot be stored.
		if notification.Type != "device_identity_mismatch" || !s.hasRecentNotification(ctx, notification, 15*time.Minute) {
			_ = s.repository.CreateNotification(ctx, notification)
		}
	}
	if s.hub != nil {
		s.hub.Publish(events.AuditRecorded, event)
	}
	return nil
}

func (s *Service) hasRecentNotification(ctx context.Context, candidate domain.Notification, window time.Duration) bool {
	values, err := s.repository.ListNotifications(ctx, 100, false)
	if err != nil {
		return false
	}
	for _, value := range values {
		if value.Type == candidate.Type && value.Resource == candidate.Resource && candidate.CreatedAt.Sub(value.CreatedAt) >= 0 && candidate.CreatedAt.Sub(value.CreatedAt) < window {
			return true
		}
	}
	return false
}

func notificationFor(event domain.AuditEvent) (domain.Notification, bool) {
	severity, title, message := "", "", ""
	switch event.Type {
	case "login_failed":
		severity, title, message = "warning", "Failed sign-in", "A sign-in attempt was denied"
	case "device_identity_mismatch":
		severity, title, message = "critical", "Device identity mismatch", "A client presented an unexpected device identity"
	case "user_registration":
		severity, title, message = "info", "Registration pending", "A new user registration requires approval"
	case "mfa_disabled", "mfa_admin_reset":
		severity, title, message = "warning", "Multi-factor authentication changed", "Multi-factor authentication was disabled or reset"
	case "api_token_create":
		severity, title, message = "info", "API token created", "A new deployment API token was created"
	case "server_control_command":
		severity, title, message = "info", "Server command queued", "An authenticated server-control command was queued"
	default:
		return domain.Notification{}, false
	}
	resource := event.TargetRustDeskID
	if resource == "" {
		resource = event.ActorUserID
	}
	return domain.Notification{ID: uuid.NewString(), Type: event.Type, Severity: severity, Title: title, Message: message, Resource: resource, CreatedAt: event.OccurredAt}, true
}
