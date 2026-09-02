package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/art-rustdesk/platform/art-api/internal/domain"
)

type clientConnectionAuditRequest struct {
	Action       string   `json:"action"`
	ConnectionID int64    `json:"conn_id"`
	ID           string   `json:"id"`
	Peer         []string `json:"peer"`
	SessionID    float64  `json:"session_id"`
	Type         int      `json:"type"`
	UUID         string   `json:"uuid"`
}

type clientFileAuditRequest struct {
	ID     string `json:"id"`
	Info   string `json:"info"`
	IsFile bool   `json:"is_file"`
	Path   string `json:"path"`
	PeerID string `json:"peer_id"`
	Type   int    `json:"type"`
	UUID   string `json:"uuid"`
}

func (s *Server) clientConnectionAudit(response http.ResponseWriter, request *http.Request) {
	var input clientConnectionAuditRequest
	if decodeInventoryJSON(request, &input, 64<<10) != nil || !validInventoryIdentity(input.ID, input.UUID) ||
		(input.Action != "" && input.Action != "new" && input.Action != "close") || len(input.Peer) > 2 {
		writeError(response, http.StatusBadRequest, "invalid connection audit")
		return
	}
	if !s.validAuditDevice(request, input.ID, input.UUID) {
		writeError(response, http.StatusForbidden, "device identity mismatch")
		return
	}
	fromPeer, fromName := "", ""
	if len(input.Peer) > 0 {
		fromPeer = strings.TrimSpace(input.Peer[0])
	}
	if len(input.Peer) > 1 {
		fromName = strings.TrimSpace(input.Peer[1])
	}
	eventType := "connection_updated"
	if input.Action == "new" {
		eventType = "connection_started"
	} else if input.Action == "close" {
		eventType = "connection_closed"
	}
	event := domain.AuditEvent{Type: eventType, ControllerDevice: fromPeer,
		TargetRustDeskID: input.ID, IP: clientIP(request), Result: "success", Metadata: map[string]any{
			"connection_id": input.ConnectionID, "session_id": strconv.FormatFloat(input.SessionID, 'f', -1, 64),
			"connection_type": input.Type, "controller_name": fromName,
		}}
	if lookup, ok := s.repository.(interface {
		AuditActorByDevice(context.Context, string, time.Time) (string, string, string, string, error)
	}); ok && fromPeer != "" {
		if userID, sessionID, username, displayName, err := lookup.AuditActorByDevice(request.Context(), fromPeer, time.Now().UTC()); err == nil {
			event.ActorUserID, event.ActorSessionID = userID, sessionID
			event.Metadata["controller_login"] = username
			event.Metadata["controller_display_name"] = firstNonEmpty(displayName, username)
		}
	}
	if err := s.audit.Record(request.Context(), event); err != nil {
		writeError(response, http.StatusInternalServerError, "connection audit unavailable")
		return
	}
	s.persistClientConnection(request.Context(), input, event, eventType)
	writeJSON(response, http.StatusOK, map[string]any{"code": 0, "message": "success", "data": ""})
}

func (s *Server) persistClientConnection(ctx context.Context, input clientConnectionAuditRequest, event domain.AuditEvent, eventType string) {
	repository, ok := s.repository.(interface {
		UpsertConnection(context.Context, domain.ConnectionRecord) error
	})
	if !ok {
		return
	}
	now := event.OccurredAt
	if now.IsZero() {
		now = time.Now().UTC()
	}
	connectionID := strconv.FormatInt(input.ConnectionID, 10)
	sessionID := strconv.FormatFloat(input.SessionID, 'f', -1, 64)
	value := domain.ConnectionRecord{Key: input.ID + ":" + event.ControllerDevice + ":" + connectionID + ":" + sessionID,
		ActorUserID: event.ActorUserID, ActorSessionID: event.ActorSessionID, ControllerDevice: event.ControllerDevice,
		ControllerName:  firstNonEmpty(stringMetadata(event.Metadata, "controller_display_name"), stringMetadata(event.Metadata, "controller_name")),
		ControllerLogin: stringMetadata(event.Metadata, "controller_login"), TargetRustDeskID: input.ID,
		ConnectionType: input.Type, IP: event.IP, StartedAt: now, LastSeenAt: now}
	if eventType == "connection_closed" {
		closed := now
		value.ClosedAt = &closed
	}
	_ = repository.UpsertConnection(ctx, value)
}

func stringMetadata(values map[string]any, key string) string {
	value, _ := values[key].(string)
	return strings.TrimSpace(value)
}

func (s *Server) clientFileAudit(response http.ResponseWriter, request *http.Request) {
	var input clientFileAuditRequest
	if decodeInventoryJSON(request, &input, 128<<10) != nil || !validInventoryIdentity(input.ID, input.UUID) ||
		len(input.Path) > 4096 || len(input.Info) > 32<<10 || len(input.PeerID) > 64 {
		writeError(response, http.StatusBadRequest, "invalid file audit")
		return
	}
	if !s.validAuditDevice(request, input.ID, input.UUID) {
		writeError(response, http.StatusForbidden, "device identity mismatch")
		return
	}
	info := map[string]any{}
	if input.Info != "" && json.Unmarshal([]byte(input.Info), &info) != nil {
		writeError(response, http.StatusBadRequest, "invalid file audit info")
		return
	}
	peerID := strings.TrimSpace(input.PeerID)
	filePath := strings.TrimSpace(input.Path)
	metadata := map[string]any{"path": filePath, "file_name": auditFileName(filePath), "is_file": input.IsFile, "transfer_type": input.Type, "client_info": info}
	event := domain.AuditEvent{Type: "file_transfer", ControllerDevice: peerID, TargetRustDeskID: input.ID,
		IP: clientIP(request), Result: "success", Metadata: metadata}
	if lookup, ok := s.repository.(interface {
		DeviceAuditLabel(context.Context, string) (string, string, error)
	}); ok {
		if hostname, alias, err := lookup.DeviceAuditLabel(request.Context(), input.ID); err == nil {
			metadata["target_hostname"], metadata["target_alias"] = hostname, alias
		}
	}
	if lookup, ok := s.repository.(interface {
		AuditActorByDevice(context.Context, string, time.Time) (string, string, string, string, error)
	}); ok && peerID != "" {
		if userID, sessionID, username, displayName, err := lookup.AuditActorByDevice(request.Context(), peerID, time.Now().UTC()); err == nil {
			event.ActorUserID, event.ActorSessionID = userID, sessionID
			metadata["controller_login"] = username
			metadata["controller_display_name"] = firstNonEmpty(displayName, username)
		}
	}
	if err := s.audit.Record(request.Context(), event); err != nil {
		writeError(response, http.StatusInternalServerError, "file audit unavailable")
		return
	}
	writeJSON(response, http.StatusOK, map[string]any{"code": 0, "message": "success", "data": ""})
}

func auditFileName(path string) string {
	path = strings.TrimRight(strings.TrimSpace(path), "/\\")
	if index := strings.LastIndexAny(path, "/\\"); index >= 0 {
		return path[index+1:]
	}
	return path
}

func (s *Server) validAuditDevice(request *http.Request, id, clientUUID string) bool {
	_, exists, mismatch, err := s.inventoryDevice(request, id, clientUUID)
	return err == nil && exists && !mismatch
}
