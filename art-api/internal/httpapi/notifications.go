package httpapi

import (
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/art-rustdesk/platform/art-api/internal/domain"
)

func (s *Server) listNotifications(response http.ResponseWriter, request *http.Request) {
	limit, _ := strconv.Atoi(request.URL.Query().Get("limit"))
	unreadOnly := request.URL.Query().Get("unread") == "true"
	values, err := s.repository.ListNotifications(request.Context(), limit, unreadOnly)
	if err != nil {
		writeError(response, http.StatusInternalServerError, "notifications unavailable")
		return
	}
	unread := 0
	for _, value := range values {
		if value.ReadAt == nil {
			unread++
		}
	}
	writeJSON(response, http.StatusOK, map[string]any{"notifications": values, "unread": unread})
}

func (s *Server) markNotificationRead(response http.ResponseWriter, request *http.Request) {
	err := s.repository.MarkNotificationRead(request.Context(), request.PathValue("notificationID"), time.Now().UTC())
	if errors.Is(err, domain.ErrNotFound) {
		writeError(response, http.StatusNotFound, "notification not found")
		return
	}
	if err != nil {
		writeError(response, http.StatusInternalServerError, "cannot update notification")
		return
	}
	response.WriteHeader(http.StatusNoContent)
}

func (s *Server) markAllNotificationsRead(response http.ResponseWriter, request *http.Request) {
	if err := s.repository.MarkAllNotificationsRead(request.Context(), time.Now().UTC()); err != nil {
		writeError(response, http.StatusInternalServerError, "cannot update notifications")
		return
	}
	response.WriteHeader(http.StatusNoContent)
}
