package httpapi

import (
	"encoding/base64"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/art-rustdesk/platform/art-api/internal/domain"
	"github.com/art-rustdesk/platform/art-api/internal/managedclient"
	"github.com/google/uuid"
)

func (s *Server) listClientProfiles(response http.ResponseWriter, request *http.Request) {
	values, err := s.repository.ListClientProfiles(request.Context())
	if err != nil {
		writeError(response, 500, "client profiles unavailable")
		return
	}
	writeJSON(response, 200, values)
}

func (s *Server) createClientProfile(response http.ResponseWriter, request *http.Request) {
	if s.managedClients == nil {
		writeError(response, 503, "managed clients unavailable")
		return
	}
	var value domain.ClientProfile
	if decodeJSON(request, &value, 128<<10) != nil {
		writeError(response, 400, "invalid client profile")
		return
	}
	value.Name, value.Description, value.Platform = strings.TrimSpace(value.Name), strings.TrimSpace(value.Description), strings.ToLower(strings.TrimSpace(value.Platform))
	if err := managedclient.ValidateProfile(value); err != nil {
		writeError(response, 400, err.Error())
		return
	}
	now := time.Now().UTC()
	value.ID, value.Version, value.CreatedAt, value.UpdatedAt = uuid.NewString(), 1, now, now
	if err := s.repository.CreateClientProfile(request.Context(), value); err != nil {
		writeError(response, 409, "client profile already exists")
		return
	}
	s.auditClientProfile(request, "client_profile_created", value.ID)
	writeJSON(response, 201, value)
}

func (s *Server) updateClientProfile(response http.ResponseWriter, request *http.Request) {
	existing, err := s.repository.FindClientProfileByID(request.Context(), request.PathValue("profileID"))
	if errors.Is(err, domain.ErrNotFound) {
		writeError(response, 404, "client profile not found")
		return
	} else if err != nil {
		writeError(response, 500, "client profile unavailable")
		return
	}
	var value domain.ClientProfile
	if decodeJSON(request, &value, 128<<10) != nil {
		writeError(response, 400, "invalid client profile")
		return
	}
	value.ID, value.CreatedAt, value.Version, value.UpdatedAt = existing.ID, existing.CreatedAt, existing.Version+1, time.Now().UTC()
	value.Name, value.Description, value.Platform = strings.TrimSpace(value.Name), strings.TrimSpace(value.Description), strings.ToLower(strings.TrimSpace(value.Platform))
	if err = managedclient.ValidateProfile(value); err != nil {
		writeError(response, 400, err.Error())
		return
	}
	if err = s.repository.UpdateClientProfile(request.Context(), value); err != nil {
		writeError(response, 500, "cannot update client profile")
		return
	}
	s.auditClientProfile(request, "client_profile_updated", value.ID)
	writeJSON(response, 200, value)
}

func (s *Server) deleteClientProfile(response http.ResponseWriter, request *http.Request) {
	id := request.PathValue("profileID")
	if err := s.repository.DeleteClientProfile(request.Context(), id); errors.Is(err, domain.ErrNotFound) {
		writeError(response, 404, "client profile not found")
		return
	} else if err != nil {
		writeError(response, 500, "cannot delete client profile")
		return
	}
	s.auditClientProfile(request, "client_profile_deleted", id)
	response.WriteHeader(204)
}

func (s *Server) clientProfileBundle(response http.ResponseWriter, request *http.Request) {
	if s.managedClients == nil {
		writeError(response, 503, "managed clients unavailable")
		return
	}
	profile, err := s.repository.FindClientProfileByID(request.Context(), request.PathValue("profileID"))
	if errors.Is(err, domain.ErrNotFound) {
		writeError(response, 404, "client profile not found")
		return
	} else if err != nil {
		writeError(response, 500, "client profile unavailable")
		return
	}
	bundle, err := s.managedClients.Bundle(profile, time.Now().UTC())
	if err != nil {
		writeError(response, 500, "bundle unavailable")
		return
	}
	response.Header().Set("Content-Disposition", `attachment; filename="rustdesk-client-profile.json"`)
	writeJSON(response, 200, bundle)
}

func (s *Server) listClientProfileAssignments(response http.ResponseWriter, request *http.Request) {
	values, err := s.repository.ListClientProfileAssignments(request.Context())
	if err != nil {
		writeError(response, 500, "assignments unavailable")
		return
	}
	writeJSON(response, 200, values)
}

func (s *Server) createClientProfileAssignment(response http.ResponseWriter, request *http.Request) {
	var value domain.ClientProfileAssignment
	if decodeJSON(request, &value, 16<<10) != nil {
		writeError(response, 400, "invalid assignment")
		return
	}
	value.ProfileID, value.ScopeType, value.ScopeID = strings.TrimSpace(value.ProfileID), strings.TrimSpace(value.ScopeType), strings.TrimSpace(value.ScopeID)
	allowed := map[string]bool{"global": true, "user": true, "user_group": true, "device_group": true, "device": true}
	if value.Priority < 1 || value.Priority > 100000 || !allowed[value.ScopeType] || (value.ScopeType == "global" && value.ScopeID != "") || (value.ScopeType != "global" && value.ScopeID == "") {
		writeError(response, 400, "invalid assignment")
		return
	}
	if _, err := s.repository.FindClientProfileByID(request.Context(), value.ProfileID); err != nil {
		writeError(response, 400, "client profile not found")
		return
	}
	if !s.validClientProfileScope(request, value.ScopeType, value.ScopeID) {
		writeError(response, 400, "assignment target not found")
		return
	}
	value.ID, value.CreatedAt = uuid.NewString(), time.Now().UTC()
	if err := s.repository.CreateClientProfileAssignment(request.Context(), value); err != nil {
		writeError(response, 500, "cannot create assignment")
		return
	}
	s.auditClientProfile(request, "client_profile_assigned", value.ProfileID)
	writeJSON(response, 201, value)
}

func (s *Server) validClientProfileScope(request *http.Request, scopeType, scopeID string) bool {
	switch scopeType {
	case "global":
		return true
	case "user":
		_, err := s.repository.FindUserByID(request.Context(), scopeID)
		return err == nil
	case "user_group", "device_group":
		group, err := s.repository.FindGroupByID(request.Context(), scopeID)
		if err != nil {
			return false
		}
		if scopeType == "user_group" {
			return group.Kind == domain.GroupKindUser
		}
		return group.Kind == domain.GroupKindDevice
	case "device":
		devices, err := s.repository.ListDevices(request.Context())
		if err != nil {
			return false
		}
		for _, device := range devices {
			if device.RustDeskID == scopeID {
				return true
			}
		}
	}
	return false
}

func (s *Server) deleteClientProfileAssignment(response http.ResponseWriter, request *http.Request) {
	if err := s.repository.DeleteClientProfileAssignment(request.Context(), request.PathValue("assignmentID")); errors.Is(err, domain.ErrNotFound) {
		writeError(response, 404, "assignment not found")
		return
	} else if err != nil {
		writeError(response, 500, "cannot delete assignment")
		return
	}
	s.auditClientProfile(request, "client_profile_unassigned", request.PathValue("assignmentID"))
	response.WriteHeader(204)
}

func (s *Server) effectiveClientProfile(response http.ResponseWriter, request *http.Request) {
	if s.managedClients == nil {
		writeError(response, 503, "managed clients unavailable")
		return
	}
	devices, err := s.repository.ListDevices(request.Context())
	if err != nil {
		writeError(response, 500, "devices unavailable")
		return
	}
	for _, device := range devices {
		if device.RustDeskID == request.PathValue("rustdeskID") {
			value, resolveErr := s.managedClients.EffectiveForDevice(request.Context(), device)
			if resolveErr != nil {
				writeError(response, 500, "client profile unavailable")
				return
			}
			writeJSON(response, 200, value)
			return
		}
	}
	writeError(response, 404, "device not found")
}

func (s *Server) auditClientProfile(request *http.Request, eventType, id string) {
	principal, _ := principalFrom(request.Context())
	_ = s.audit.Record(request.Context(), domain.AuditEvent{Type: eventType, ActorUserID: principal.User.ID, ActorSessionID: principal.Session.ID, Result: "success", Metadata: map[string]any{"client_profile_id": id}})
}

func (s *Server) listClientBuilds(response http.ResponseWriter, request *http.Request) {
	values, err := s.repository.ListClientBuilds(request.Context(), 100)
	if err != nil {
		writeError(response, 500, "client builds unavailable")
		return
	}
	writeJSON(response, 200, values)
}
func (s *Server) createClientBuild(response http.ResponseWriter, request *http.Request) {
	if s.managedClients == nil {
		writeError(response, 503, "managed clients unavailable")
		return
	}
	var input struct {
		ProfileID    string `json:"profile_id"`
		TargetOS     string `json:"target_os"`
		Architecture string `json:"architecture"`
		Format       string `json:"format"`
	}
	if decodeJSON(request, &input, 8<<10) != nil {
		writeError(response, 400, "invalid build")
		return
	}
	principal, _ := principalFrom(request.Context())
	value, err := s.managedClients.CreateBuild(request.Context(), managedclient.BuildRequest{ProfileID: input.ProfileID, TargetOS: input.TargetOS, Architecture: input.Architecture, Format: input.Format, CreatedBy: principal.User.ID})
	if err != nil {
		writeError(response, 400, err.Error())
		return
	}
	_ = s.audit.Record(request.Context(), domain.AuditEvent{Type: "client_build_created", ActorUserID: principal.User.ID, ActorSessionID: principal.Session.ID, Result: value.Status, Metadata: map[string]any{"build_id": value.ID, "profile_id": value.ProfileID, "target_os": value.TargetOS, "architecture": value.Architecture, "format": value.Format}})
	writeJSON(response, 201, value)
}
func (s *Server) clientBuildArtifact(response http.ResponseWriter, request *http.Request) {
	value, err := s.repository.FindClientBuildByID(request.Context(), request.PathValue("buildID"))
	if errors.Is(err, domain.ErrNotFound) {
		writeError(response, 404, "client build not found")
		return
	} else if err != nil {
		writeError(response, 500, "client build unavailable")
		return
	}
	if value.Status != "ready" || value.Artifact == "" {
		writeError(response, 409, "client artifact is not ready")
		return
	}
	artifact := []byte(value.Artifact)
	if strings.HasPrefix(value.Artifact, "base64:") {
		decoded, decodeErr := base64.StdEncoding.DecodeString(strings.TrimPrefix(value.Artifact, "base64:"))
		if decodeErr != nil {
			writeError(response, 500, "client artifact unavailable")
			return
		}
		artifact = decoded
	}
	mediaType := value.MediaType
	if mediaType == "" {
		mediaType = "application/octet-stream"
	}
	response.Header().Set("Content-Type", mediaType)
	response.Header().Set("Content-Disposition", `attachment; filename="`+value.ArtifactName+`"`)
	response.Header().Set("X-Content-SHA256", value.SHA256)
	response.WriteHeader(200)
	_, _ = response.Write(artifact)
}

func (s *Server) cancelClientBuild(response http.ResponseWriter, request *http.Request) {
	value, err := s.repository.FindClientBuildByID(request.Context(), request.PathValue("buildID"))
	if err != nil {
		writeError(response, 404, "client build not found")
		return
	}
	if !map[string]bool{"queued": true, "leased": true, "building": true, "uploading": true}[value.Status] {
		writeError(response, 409, "client build cannot be cancelled")
		return
	}
	now := time.Now().UTC()
	value.Status, value.Error, value.LeaseUntil, value.CompletedAt = "cancelled", "", nil, &now
	if err = s.repository.UpdateClientBuild(request.Context(), value); err != nil {
		writeError(response, 500, "client build update failed")
		return
	}
	s.auditClientBuild(request, "client_build_cancelled", value)
	writeJSON(response, 200, value)
}

func (s *Server) retryClientBuild(response http.ResponseWriter, request *http.Request) {
	value, err := s.repository.FindClientBuildByID(request.Context(), request.PathValue("buildID"))
	if err != nil {
		writeError(response, 404, "client build not found")
		return
	}
	if !map[string]bool{"failed": true, "cancelled": true}[value.Status] || value.Attempts >= 5 {
		writeError(response, 409, "client build cannot be retried")
		return
	}
	value.Status, value.Error, value.WorkerID = "queued", "", ""
	value.StartedAt, value.LeaseUntil, value.CompletedAt = nil, nil, nil
	if err = s.repository.UpdateClientBuild(request.Context(), value); err != nil {
		writeError(response, 500, "client build update failed")
		return
	}
	s.auditClientBuild(request, "client_build_retried", value)
	writeJSON(response, 200, value)
}

func (s *Server) auditClientBuild(request *http.Request, eventType string, value domain.ClientBuild) {
	principal, _ := principalFrom(request.Context())
	_ = s.audit.Record(request.Context(), domain.AuditEvent{Type: eventType, ActorUserID: principal.User.ID, ActorSessionID: principal.Session.ID, Result: value.Status, Metadata: map[string]any{"build_id": value.ID, "profile_id": value.ProfileID}})
}
