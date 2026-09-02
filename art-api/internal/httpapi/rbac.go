package httpapi

import (
	"errors"
	"net/http"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/art-rustdesk/platform/art-api/internal/domain"
	"github.com/art-rustdesk/platform/art-api/internal/events"
)

var roleIDPattern = regexp.MustCompile(`^[a-z][a-z0-9_-]{1,63}$`)

type roleRequest struct {
	ID          domain.Role `json:"id"`
	Name        string      `json:"name"`
	Description string      `json:"description"`
	Permissions []string    `json:"permissions"`
}

func (s *Server) listPermissions(response http.ResponseWriter, _ *http.Request) {
	writeJSON(response, http.StatusOK, domain.AvailablePermissions)
}

func (s *Server) listRoles(response http.ResponseWriter, request *http.Request) {
	roles, err := s.repository.ListRoles(request.Context())
	if err != nil {
		writeError(response, 500, "roles unavailable")
		return
	}
	writeJSON(response, 200, roles)
}

func normalizeRole(input *roleRequest) bool {
	input.ID = domain.Role(strings.ToLower(strings.TrimSpace(string(input.ID))))
	input.Name, input.Description = strings.TrimSpace(input.Name), strings.TrimSpace(input.Description)
	if !roleIDPattern.MatchString(string(input.ID)) || len(input.Name) < 2 || len(input.Name) > 128 || len(input.Description) > 512 || len(input.Permissions) > len(domain.AvailablePermissions) {
		return false
	}
	seen := make(map[string]bool, len(input.Permissions))
	permissions := make([]string, 0, len(input.Permissions))
	for _, permission := range input.Permissions {
		permission = strings.TrimSpace(permission)
		if permission == domain.PermissionAll || !slices.Contains(domain.AvailablePermissions, permission) {
			return false
		}
		if !seen[permission] {
			seen[permission] = true
			permissions = append(permissions, permission)
		}
	}
	slices.Sort(permissions)
	input.Permissions = permissions
	return true
}

func canGrant(user domain.User, permissions []string) bool {
	if user.Role == domain.RoleAdmin {
		return true
	}
	for _, permission := range permissions {
		if !hasPermission(user, permission) {
			return false
		}
	}
	return true
}

func (s *Server) createRole(response http.ResponseWriter, request *http.Request) {
	principal, _ := principalFrom(request.Context())
	var input roleRequest
	if decodeJSON(request, &input, 16<<10) != nil || !normalizeRole(&input) || !canGrant(principal.User, input.Permissions) || input.ID == domain.RoleAdmin || input.ID == domain.RoleUser {
		writeError(response, 400, "invalid role")
		return
	}
	now := time.Now().UTC()
	role := domain.RoleDefinition{ID: input.ID, Name: input.Name, Description: input.Description, Permissions: input.Permissions, CreatedAt: now, UpdatedAt: now}
	if err := s.repository.CreateRole(request.Context(), role); err != nil {
		writeError(response, 409, "role already exists")
		return
	}
	_ = s.audit.Record(request.Context(), domain.AuditEvent{Type: "role_create", ActorUserID: principal.User.ID, Result: "success", Metadata: map[string]any{"role_id": role.ID}})
	writeJSON(response, 201, role)
}

func (s *Server) updateRole(response http.ResponseWriter, request *http.Request) {
	principal, _ := principalFrom(request.Context())
	var input roleRequest
	input.ID = domain.Role(request.PathValue("roleID"))
	if decodeJSON(request, &input, 16<<10) != nil {
		writeError(response, 400, "invalid role")
		return
	}
	input.ID = domain.Role(request.PathValue("roleID"))
	if !normalizeRole(&input) || !canGrant(principal.User, input.Permissions) {
		writeError(response, 400, "invalid role")
		return
	}
	existing, err := s.repository.FindRoleByID(request.Context(), input.ID)
	if err != nil || existing.System {
		writeError(response, 409, "system role cannot be changed")
		return
	}
	existing.Name, existing.Description, existing.Permissions, existing.UpdatedAt = input.Name, input.Description, input.Permissions, time.Now().UTC()
	if err = s.repository.UpdateRole(request.Context(), existing); err != nil {
		writeError(response, 404, "role not found")
		return
	}
	users, _ := s.repository.ListUsers(request.Context())
	for _, user := range users {
		if user.Role == existing.ID {
			_, _ = s.auth.ForceRelogin(request.Context(), user.ID)
		}
	}
	_ = s.audit.Record(request.Context(), domain.AuditEvent{Type: "role_update", ActorUserID: principal.User.ID, Result: "success", Metadata: map[string]any{"role_id": existing.ID}})
	writeJSON(response, 200, existing)
}

func (s *Server) deleteRole(response http.ResponseWriter, request *http.Request) {
	principal, _ := principalFrom(request.Context())
	id := domain.Role(request.PathValue("roleID"))
	users, _ := s.repository.ListUsers(request.Context())
	if err := s.repository.DeleteRole(request.Context(), id); errors.Is(err, domain.ErrNotFound) {
		writeError(response, 404, "role not found")
		return
	} else if err != nil {
		writeError(response, 409, err.Error())
		return
	}
	for _, user := range users {
		if user.Role == id {
			updated, err := s.repository.FindUserByID(request.Context(), user.ID)
			if err == nil {
				s.hub.Publish(events.UserUpdated, updated)
				s.hub.Publish(events.SessionRevokedAll, map[string]string{"user_id": user.ID})
			}
		}
	}
	_ = s.audit.Record(request.Context(), domain.AuditEvent{Type: "role_delete", ActorUserID: principal.User.ID, Result: "success", Metadata: map[string]any{"role_id": id}})
	response.WriteHeader(http.StatusNoContent)
}
