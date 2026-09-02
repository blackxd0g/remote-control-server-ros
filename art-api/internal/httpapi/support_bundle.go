package httpapi

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/art-rustdesk/platform/art-api/internal/config"
	"github.com/art-rustdesk/platform/art-api/internal/domain"
)

type supportClusterRepository interface {
	ListClusterNodes(context.Context, time.Time) ([]domain.ClusterNode, error)
	ListClusterLeases(context.Context, time.Time) ([]domain.ClusterLease, error)
}

type safeAuditEvent struct {
	Type       string    `json:"type"`
	Result     string    `json:"result"`
	OccurredAt time.Time `json:"occurred_at"`
}

type safeClusterNode struct {
	Service    string    `json:"service"`
	Version    string    `json:"version"`
	StartedAt  time.Time `json:"started_at"`
	LastSeenAt time.Time `json:"last_seen_at"`
	LeaseCount int       `json:"lease_count"`
}

func (s *Server) supportBundle(response http.ResponseWriter, request *http.Request) {
	now := time.Now().UTC()
	buffer := bytes.NewBuffer(nil)
	archive := zip.NewWriter(buffer)
	manifest := map[string]any{"product": "Remote Control Server RouterOS", "version": envValue("RDS_VERSION", envValue("ART_VERSION", config.BuildVersion)), "generated_at": now, "bundle_schema": "remote-control-server/support/v1", "redacted": true, "auth_cache_revision": s.hub.Revision(), "trusted_proxy_count": len(s.trustedProxies)}
	_ = writeSupportJSON(archive, "manifest.json", manifest)
	configuration := map[string]any{}
	if s.configuration != nil {
		value := s.configuration.Values()
		configuration = map[string]any{"require_login": value.RequireLogin, "require_device_deployment": value.RequireDeviceDeployment, "registration_enabled": value.RegistrationEnabled, "registration_auto_approve": value.RegistrationAutoApprove, "access_token_ttl": value.AccessTokenTTL.String(), "session_ttl": value.SessionTTL.String(), "mfa_mode": value.MFAMode, "password_minimum_length": value.PasswordMinimumLength, "password_require_upper": value.PasswordRequireUpper, "password_require_lower": value.PasswordRequireLower, "password_require_number": value.PasswordRequireNumber, "password_require_special": value.PasswordRequireSpecial}
	}
	_ = writeSupportJSON(archive, "configuration.json", configuration)
	if repository, ok := s.repository.(supportClusterRepository); ok {
		nodes, _ := repository.ListClusterNodes(request.Context(), now.Add(-24*time.Hour))
		leases, _ := repository.ListClusterLeases(request.Context(), now)
		safeNodes := make([]safeClusterNode, 0, len(nodes))
		for _, node := range nodes {
			safeNodes = append(safeNodes, safeClusterNode{Service: node.Service, Version: node.Version, StartedAt: node.StartedAt, LastSeenAt: node.LastSeenAt, LeaseCount: node.LeaseCount})
		}
		_ = writeSupportJSON(archive, "cluster.json", map[string]any{"nodes": safeNodes, "active_lease_count": len(leases)})
	}
	auditEvents, _ := s.repository.ListAudit(request.Context(), 200)
	safeEvents := make([]safeAuditEvent, 0, len(auditEvents))
	for _, event := range auditEvents {
		safeEvents = append(safeEvents, safeAuditEvent{Type: event.Type, Result: event.Result, OccurredAt: event.OccurredAt})
	}
	_ = writeSupportJSON(archive, "recent-audit-redacted.json", safeEvents)
	users, userErr := s.repository.CountUsers(request.Context())
	devices, deviceErr := s.repository.ListDevices(request.Context())
	_, sessions, sessionErr := s.repository.ListAuthState(request.Context(), now)
	_ = writeSupportJSON(archive, "inventory.json", map[string]any{"users": users, "devices": len(devices), "active_sessions": len(sessions), "database_responsive": userErr == nil && deviceErr == nil && sessionErr == nil})
	if err := archive.Close(); err != nil {
		writeError(response, 500, "support bundle unavailable")
		return
	}
	principal, _ := principalFrom(request.Context())
	_ = s.audit.Record(request.Context(), domain.AuditEvent{Type: "support_bundle_download", ActorUserID: principal.User.ID, ActorSessionID: principal.Session.ID, Result: "success"})
	filename := fmt.Sprintf("remote-control-server-support-%s.zip", now.Format("20060102-150405"))
	response.Header().Set("Content-Type", "application/zip")
	response.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s"`, filename))
	response.Header().Set("Cache-Control", "no-store")
	response.WriteHeader(http.StatusOK)
	_, _ = response.Write(buffer.Bytes())
}

func writeSupportJSON(archive *zip.Writer, name string, value any) error {
	entry, err := archive.Create(name)
	if err != nil {
		return err
	}
	encoder := json.NewEncoder(entry)
	encoder.SetIndent("", "  ")
	encoder.SetEscapeHTML(true)
	return encoder.Encode(value)
}
