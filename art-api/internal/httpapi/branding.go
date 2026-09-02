package httpapi

import (
	"bytes"
	"encoding/xml"
	"errors"
	"image"
	_ "image/png"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/art-rustdesk/platform/art-api/internal/domain"
)

const maxBrandingLogoSize = 2 << 20

func (s *Server) brandingLogoPath() string {
	if s.brandingDir == "" {
		return ""
	}
	return filepath.Join(s.brandingDir, "logo.custom")
}

func (s *Server) hasBrandingLogo() bool {
	path := s.brandingLogoPath()
	if path == "" {
		return false
	}
	info, err := os.Stat(path)
	return err == nil && info.Mode().IsRegular() && info.Size() > 0 && info.Size() <= maxBrandingLogoSize
}

func (s *Server) brandingLogo(response http.ResponseWriter, _ *http.Request) {
	path := s.brandingLogoPath()
	if path == "" {
		http.NotFound(response, nil)
		return
	}
	data, err := os.ReadFile(path)
	if err != nil {
		http.NotFound(response, nil)
		return
	}
	mediaType, err := validateBrandingLogo(data)
	if err != nil {
		http.NotFound(response, nil)
		return
	}
	response.Header().Set("Content-Type", mediaType)
	response.Header().Set("Cache-Control", "no-cache, no-store, must-revalidate")
	response.Header().Set("Content-Length", strconv.Itoa(len(data)))
	_, _ = response.Write(data)
}

func (s *Server) uploadBrandingLogo(response http.ResponseWriter, request *http.Request) {
	if s.brandingDir == "" {
		writeError(response, http.StatusServiceUnavailable, "branding storage is unavailable")
		return
	}
	request.Body = http.MaxBytesReader(response, request.Body, maxBrandingLogoSize+(64<<10))
	if err := request.ParseMultipartForm(maxBrandingLogoSize); err != nil {
		writeError(response, http.StatusBadRequest, "invalid logo upload")
		return
	}
	file, header, err := request.FormFile("logo")
	if err != nil {
		writeError(response, http.StatusBadRequest, "logo file is required")
		return
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, maxBrandingLogoSize+1))
	if err != nil || len(data) == 0 || len(data) > maxBrandingLogoSize {
		writeError(response, http.StatusBadRequest, "logo must not exceed 2 MiB")
		return
	}
	mediaType, err := validateBrandingLogo(data)
	if err != nil {
		writeError(response, http.StatusBadRequest, err.Error())
		return
	}
	extension := strings.ToLower(filepath.Ext(header.Filename))
	if (mediaType == "image/png" && extension != ".png") || (mediaType == "image/svg+xml" && extension != ".svg") {
		writeError(response, http.StatusBadRequest, "file extension does not match logo format")
		return
	}
	if err = os.MkdirAll(s.brandingDir, 0o750); err != nil {
		writeError(response, http.StatusInternalServerError, "branding storage is unavailable")
		return
	}
	if err = replaceBrandingFile(s.brandingLogoPath(), data); err != nil {
		writeError(response, http.StatusInternalServerError, "logo could not be saved")
		return
	}
	principal, _ := principalFrom(request.Context())
	_ = s.audit.Record(request.Context(), domain.AuditEvent{Type: "console_logo_updated", ActorUserID: principal.User.ID, ActorSessionID: principal.Session.ID, Result: "success", Metadata: map[string]any{"media_type": mediaType, "bytes": len(data)}})
	writeJSON(response, http.StatusOK, map[string]any{"custom_logo": true, "logo_url": "/api/branding/logo", "media_type": mediaType})
}

func (s *Server) deleteBrandingLogo(response http.ResponseWriter, request *http.Request) {
	path := s.brandingLogoPath()
	if path == "" {
		writeError(response, http.StatusServiceUnavailable, "branding storage is unavailable")
		return
	}
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		writeError(response, http.StatusInternalServerError, "logo could not be reset")
		return
	}
	principal, _ := principalFrom(request.Context())
	_ = s.audit.Record(request.Context(), domain.AuditEvent{Type: "console_logo_reset", ActorUserID: principal.User.ID, ActorSessionID: principal.Session.ID, Result: "success"})
	response.WriteHeader(http.StatusNoContent)
}

func replaceBrandingFile(path string, data []byte) error {
	temporary, err := os.CreateTemp(filepath.Dir(path), ".logo-*")
	if err != nil {
		return err
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	if err = temporary.Chmod(0o640); err == nil {
		_, err = temporary.Write(data)
	}
	if err == nil {
		err = temporary.Sync()
	}
	closeErr := temporary.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	if err = os.Rename(temporaryPath, path); err == nil {
		return nil
	}
	if removeErr := os.Remove(path); removeErr != nil && !errors.Is(removeErr, os.ErrNotExist) {
		return err
	}
	return os.Rename(temporaryPath, path)
}

func validateBrandingLogo(data []byte) (string, error) {
	if len(data) == 0 || len(data) > maxBrandingLogoSize {
		return "", errors.New("logo must not exceed 2 MiB")
	}
	if bytes.HasPrefix(data, []byte("\x89PNG\r\n\x1a\n")) {
		configuration, _, err := image.DecodeConfig(bytes.NewReader(data))
		if err != nil || configuration.Width < 1 || configuration.Height < 1 || configuration.Width > 8192 || configuration.Height > 8192 || int64(configuration.Width)*int64(configuration.Height) > 32_000_000 {
			return "", errors.New("invalid or oversized PNG logo")
		}
		return "image/png", nil
	}
	decoder := xml.NewDecoder(bytes.NewReader(data))
	decoder.Strict = true
	rootSeen := false
	for {
		token, err := decoder.Token()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return "", errors.New("invalid SVG logo")
		}
		switch value := token.(type) {
		case xml.Directive:
			return "", errors.New("SVG directives are not allowed")
		case xml.StartElement:
			name := strings.ToLower(value.Name.Local)
			if !rootSeen {
				if name != "svg" {
					return "", errors.New("invalid SVG logo")
				}
				rootSeen = true
			}
			if map[string]bool{"script": true, "foreignobject": true, "iframe": true, "object": true, "embed": true}[name] {
				return "", errors.New("unsafe SVG content")
			}
			for _, attribute := range value.Attr {
				attributeName := strings.ToLower(attribute.Name.Local)
				attributeValue := strings.TrimSpace(strings.ToLower(attribute.Value))
				if strings.HasPrefix(attributeName, "on") || ((attributeName == "href" || attributeName == "src") && attributeValue != "" && !strings.HasPrefix(attributeValue, "#")) || strings.Contains(attributeValue, "javascript:") {
					return "", errors.New("unsafe SVG content")
				}
			}
		}
	}
	if !rootSeen {
		return "", errors.New("invalid SVG logo")
	}
	return "image/svg+xml", nil
}
