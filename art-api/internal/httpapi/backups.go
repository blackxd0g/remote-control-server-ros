package httpapi

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/art-rustdesk/platform/art-api/internal/domain"
)

func (s *Server) listManagedBackups(response http.ResponseWriter, request *http.Request) {
	if s.backups == nil {
		writeError(response, http.StatusConflict, "managed backups require SQLite")
		return
	}
	values, err := s.backups.List(request.Context())
	if err != nil {
		writeError(response, http.StatusInternalServerError, "backups unavailable")
		return
	}
	writeJSON(response, http.StatusOK, values)
}

func (s *Server) createManagedBackup(response http.ResponseWriter, request *http.Request) {
	if s.backups == nil {
		writeError(response, http.StatusConflict, "managed backups require SQLite")
		return
	}
	principal, _ := principalFrom(request.Context())
	value, err := s.backups.Create(request.Context())
	if err != nil {
		writeError(response, http.StatusInternalServerError, "backup failed")
		return
	}
	_ = s.audit.Record(request.Context(), domain.AuditEvent{Type: "database_backup_create", ActorUserID: principal.User.ID, ActorSessionID: principal.Session.ID, Result: "success", Metadata: map[string]any{"name": value.Name, "size_bytes": value.SizeBytes}})
	writeJSON(response, http.StatusCreated, value)
}

func (s *Server) downloadManagedBackup(response http.ResponseWriter, request *http.Request) {
	if s.backups == nil {
		writeError(response, http.StatusConflict, "managed backups require SQLite")
		return
	}
	path, err := s.backups.Path(request.PathValue("name"))
	if err != nil {
		writeError(response, http.StatusNotFound, "backup not found")
		return
	}
	response.Header().Set("Content-Type", "application/vnd.sqlite3")
	response.Header().Set("Content-Disposition", `attachment; filename="`+request.PathValue("name")+`"`)
	response.Header().Set("Cache-Control", "no-store")
	http.ServeFile(response, request, path)
}

func (s *Server) deleteManagedBackup(response http.ResponseWriter, request *http.Request) {
	if s.backups == nil {
		writeError(response, http.StatusConflict, "managed backups require SQLite")
		return
	}
	principal, _ := principalFrom(request.Context())
	name := request.PathValue("name")
	err := s.backups.Delete(name)
	if errors.Is(err, domain.ErrNotFound) {
		writeError(response, http.StatusNotFound, "backup not found")
		return
	} else if err != nil {
		writeError(response, http.StatusInternalServerError, "backup deletion failed")
		return
	}
	_ = s.audit.Record(request.Context(), domain.AuditEvent{Type: "database_backup_delete", ActorUserID: principal.User.ID, ActorSessionID: principal.Session.ID, Result: "success", Metadata: map[string]any{"name": name}})
	response.WriteHeader(http.StatusNoContent)
}

func (s *Server) restoreStatus(response http.ResponseWriter, _ *http.Request) {
	pending := false
	intervalSeconds, retention := int64(0), 0
	if s.backups != nil {
		pending = s.backups.RestorePending()
		interval, value := s.backups.Policy()
		intervalSeconds, retention = int64(interval.Seconds()), value
	}
	writeJSON(response, http.StatusOK, map[string]any{"pending": pending, "interval_seconds": intervalSeconds, "retention": retention})
}

func (s *Server) stageRestore(response http.ResponseWriter, request *http.Request) {
	if s.backups == nil {
		writeError(response, http.StatusConflict, "managed restore requires SQLite")
		return
	}
	if request.ContentLength > 512<<20 {
		writeError(response, http.StatusRequestEntityTooLarge, "backup is too large")
		return
	}
	principal, _ := principalFrom(request.Context())
	inspection, err := s.backups.StageRestore(request.Context(), request.Body)
	if err != nil {
		_ = s.audit.Record(request.Context(), domain.AuditEvent{Type: "database_restore_stage", ActorUserID: principal.User.ID, ActorSessionID: principal.Session.ID, Result: "denied", Reason: "invalid_backup"})
		writeError(response, http.StatusUnprocessableEntity, "invalid or incompatible SQLite backup")
		return
	}
	_ = s.audit.Record(request.Context(), domain.AuditEvent{Type: "database_restore_stage", ActorUserID: principal.User.ID, ActorSessionID: principal.Session.ID, Result: "success", Metadata: map[string]any{"size_bytes": inspection.SizeBytes, "users": inspection.Users, "devices": inspection.Devices, "sessions": inspection.Sessions}})
	writeJSON(response, http.StatusAccepted, map[string]any{"pending": true, "restart_required": true, "inspection": inspection})
}

func (s *Server) cancelRestore(response http.ResponseWriter, request *http.Request) {
	if s.backups == nil {
		writeError(response, http.StatusConflict, "managed restore requires SQLite")
		return
	}
	if err := s.backups.CancelRestore(); err != nil {
		writeError(response, http.StatusInternalServerError, "restore cancellation failed")
		return
	}
	principal, _ := principalFrom(request.Context())
	_ = s.audit.Record(request.Context(), domain.AuditEvent{Type: "database_restore_cancel", ActorUserID: principal.User.ID, ActorSessionID: principal.Session.ID, Result: "success"})
	response.Header().Set("Content-Length", strconv.Itoa(0))
	response.WriteHeader(http.StatusNoContent)
}
