package httpapi

import (
	"bytes"
	"errors"
	"image"
	_ "image/jpeg"
	_ "image/png"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/art-rustdesk/platform/art-api/internal/domain"
)

const maxAvatarSize = 2 << 20

func (s *Server) avatarDir() string { return filepath.Join(s.brandingDir, "avatars") }
func (s *Server) avatarPath(userID string) string {
	return filepath.Join(s.avatarDir(), userID+".avatar")
}
func (s *Server) hasGlobalAvatar() bool {
	info, err := os.Stat(s.avatarPath("global"))
	return err == nil && info.Mode().IsRegular() && info.Size() > 0
}

func validAvatarID(value string) bool {
	if value == "global" {
		return true
	}
	if value == "" || len(value) > 128 {
		return false
	}
	for _, character := range value {
		if !(character == '-' || character >= '0' && character <= '9' || character >= 'a' && character <= 'z' || character >= 'A' && character <= 'Z') {
			return false
		}
	}
	return true
}

func validateAvatar(data []byte) (string, error) {
	mediaType := http.DetectContentType(data)
	if mediaType != "image/png" && mediaType != "image/jpeg" {
		return "", errors.New("avatar must be PNG or JPEG")
	}
	configuration, _, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil || configuration.Width < 1 || configuration.Height < 1 || configuration.Width > 4096 || configuration.Height > 4096 || int64(configuration.Width)*int64(configuration.Height) > 16_000_000 {
		return "", errors.New("invalid or oversized avatar")
	}
	return mediaType, nil
}

func (s *Server) userAvatar(response http.ResponseWriter, request *http.Request) {
	id := request.PathValue("userID")
	if !validAvatarID(id) {
		http.NotFound(response, request)
		return
	}
	path := s.avatarPath(id)
	data, err := os.ReadFile(path)
	if err != nil && id != "global" {
		data, err = os.ReadFile(s.avatarPath("global"))
	}
	if err != nil {
		http.NotFound(response, request)
		return
	}
	mediaType, err := validateAvatar(data)
	if err != nil {
		http.NotFound(response, request)
		return
	}
	response.Header().Set("Content-Type", mediaType)
	response.Header().Set("Cache-Control", "private, max-age=300")
	response.Header().Set("Content-Length", strconv.Itoa(len(data)))
	_, _ = response.Write(data)
}

func (s *Server) readAvatarUpload(response http.ResponseWriter, request *http.Request) ([]byte, string, bool) {
	request.Body = http.MaxBytesReader(response, request.Body, maxAvatarSize+(64<<10))
	if request.ParseMultipartForm(maxAvatarSize) != nil {
		writeError(response, 400, "invalid avatar upload")
		return nil, "", false
	}
	file, header, err := request.FormFile("avatar")
	if err != nil {
		writeError(response, 400, "avatar file is required")
		return nil, "", false
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, maxAvatarSize+1))
	if err != nil || len(data) == 0 || len(data) > maxAvatarSize {
		writeError(response, 400, "avatar must not exceed 2 MiB")
		return nil, "", false
	}
	mediaType, err := validateAvatar(data)
	if err != nil {
		writeError(response, 400, err.Error())
		return nil, "", false
	}
	extension := strings.ToLower(filepath.Ext(header.Filename))
	if mediaType == "image/png" && extension != ".png" || mediaType == "image/jpeg" && extension != ".jpg" && extension != ".jpeg" {
		writeError(response, 400, "file extension does not match avatar format")
		return nil, "", false
	}
	return data, mediaType, true
}

func (s *Server) saveAvatar(response http.ResponseWriter, request *http.Request, userID, eventType string) {
	data, mediaType, ok := s.readAvatarUpload(response, request)
	if !ok {
		return
	}
	if os.MkdirAll(s.avatarDir(), 0o750) != nil || replaceBrandingFile(s.avatarPath(userID), data) != nil {
		writeError(response, 500, "avatar could not be saved")
		return
	}
	principal, _ := principalFrom(request.Context())
	_ = s.audit.Record(request.Context(), domain.AuditEvent{Type: eventType, ActorUserID: principal.User.ID, ActorSessionID: principal.Session.ID, Result: "success", Metadata: map[string]any{"target_user_id": userID, "media_type": mediaType, "bytes": len(data)}})
	writeJSON(response, 200, map[string]any{"avatar": "/api/avatar/" + userID})
}
func (s *Server) removeAvatar(response http.ResponseWriter, request *http.Request, userID, eventType string) {
	if err := os.Remove(s.avatarPath(userID)); err != nil && !errors.Is(err, os.ErrNotExist) {
		writeError(response, 500, "avatar could not be removed")
		return
	}
	principal, _ := principalFrom(request.Context())
	_ = s.audit.Record(request.Context(), domain.AuditEvent{Type: eventType, ActorUserID: principal.User.ID, ActorSessionID: principal.Session.ID, Result: "success", Metadata: map[string]any{"target_user_id": userID}})
	response.WriteHeader(204)
}
func (s *Server) uploadMyAvatar(w http.ResponseWriter, r *http.Request) {
	p, _ := principalFrom(r.Context())
	s.saveAvatar(w, r, p.User.ID, "user_avatar_updated")
}
func (s *Server) deleteMyAvatar(w http.ResponseWriter, r *http.Request) {
	p, _ := principalFrom(r.Context())
	s.removeAvatar(w, r, p.User.ID, "user_avatar_removed")
}
func (s *Server) uploadAdminUserAvatar(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("userID")
	if _, err := s.repository.FindUserByID(r.Context(), id); err != nil {
		writeError(w, 404, "user not found")
		return
	}
	s.saveAvatar(w, r, id, "user_avatar_updated")
}
func (s *Server) deleteAdminUserAvatar(w http.ResponseWriter, r *http.Request) {
	s.removeAvatar(w, r, r.PathValue("userID"), "user_avatar_removed")
}
func (s *Server) uploadGlobalAvatar(w http.ResponseWriter, r *http.Request) {
	s.saveAvatar(w, r, "global", "global_avatar_updated")
}
func (s *Server) deleteGlobalAvatar(w http.ResponseWriter, r *http.Request) {
	s.removeAvatar(w, r, "global", "global_avatar_removed")
}
