package httpapi

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/art-rustdesk/platform/art-api/internal/domain"
	strategyservice "github.com/art-rustdesk/platform/art-api/internal/strategy"
)

const sysinfoVersion = "1"

type heartbeatRequest struct {
	ID          string `json:"id"`
	UUID        string `json:"uuid"`
	Version     int64  `json:"ver"`
	ModifiedAt  int64  `json:"modified_at"`
	Connections []int  `json:"conns"`
}

type sysinfoRequest struct {
	ID       string `json:"id"`
	UUID     string `json:"uuid"`
	CPU      string `json:"cpu"`
	Hostname string `json:"hostname"`
	Memory   string `json:"memory"`
	OS       string `json:"os"`
	Username string `json:"username"`
	Version  string `json:"version"`
}

func (s *Server) clientHeartbeat(response http.ResponseWriter, request *http.Request) {
	var input heartbeatRequest
	if decodeInventoryJSON(request, &input, 32<<10) != nil || !validInventoryIdentity(input.ID, input.UUID) {
		writeError(response, http.StatusBadRequest, "invalid device heartbeat")
		return
	}
	now := time.Now().UTC()
	device, exists, mismatch, err := s.inventoryDevice(request, input.ID, input.UUID)
	if err != nil {
		writeError(response, http.StatusInternalServerError, "device inventory unavailable")
		return
	}
	if mismatch && device.Deployed {
		_ = s.audit.Record(request.Context(), domain.AuditEvent{Type: "device_identity_mismatch", TargetRustDeskID: input.ID,
			IP: clientIP(request), Result: "denied", Reason: "uuid_mismatch"})
		writeError(response, http.StatusForbidden, "device identity mismatch")
		return
	}
	device.RustDeskID, device.ClientUUID, device.Online, device.LastSeen = input.ID, input.UUID, true, now
	device.LastSeenIP = clientIP(request)
	if device.CreatedAt.IsZero() {
		device.CreatedAt = now
	}
	if err := s.repository.UpsertDevice(request.Context(), device); err != nil {
		writeError(response, http.StatusInternalServerError, "device heartbeat failed")
		return
	}
	strategyResult, strategyErr := s.strategies.EffectiveForDevice(request.Context(), device)
	if strategyErr != nil {
		writeError(response, http.StatusInternalServerError, "device strategy unavailable")
		return
	}
	if s.managedClients != nil {
		profileResult, profileErr := s.managedClients.EffectiveForDevice(request.Context(), device)
		if profileErr != nil {
			writeError(response, http.StatusInternalServerError, "managed client profile unavailable")
			return
		}
		for key, value := range strategyservice.ClientOptions(profileResult.Settings) {
			strategyResult.ConfigOptions[key] = value
		}
		if profileResult.ModifiedAt > strategyResult.ModifiedAt {
			strategyResult.ModifiedAt = profileResult.ModifiedAt
		}
	}
	payload := map[string]any{"modified_at": strategyResult.ModifiedAt}
	if input.ModifiedAt != strategyResult.ModifiedAt {
		payload["strategy"] = map[string]any{"config_options": strategyResult.ConfigOptions, "extra": map[string]string{}}
	}
	if !exists || device.Hostname == "" || device.Platform == "" || device.Version == "" {
		payload["sysinfo"] = true
	}
	writeJSON(response, http.StatusOK, payload)
}

func (s *Server) clientSysinfo(response http.ResponseWriter, request *http.Request) {
	var input sysinfoRequest
	if decodeInventoryJSON(request, &input, 64<<10) != nil || !validInventoryIdentity(input.ID, input.UUID) ||
		len(input.Hostname) > 512 || len(input.OS) > 256 || len(input.Username) > 512 || len(input.Version) > 128 ||
		len(input.CPU) > 2048 || len(input.Memory) > 512 {
		writeError(response, http.StatusBadRequest, "invalid device sysinfo")
		return
	}
	device, exists, mismatch, err := s.inventoryDevice(request, input.ID, input.UUID)
	if err != nil {
		writeError(response, http.StatusInternalServerError, "device inventory unavailable")
		return
	}
	if mismatch && device.Deployed {
		_ = s.audit.Record(request.Context(), domain.AuditEvent{Type: "device_identity_mismatch", TargetRustDeskID: input.ID,
			IP: clientIP(request), Result: "denied", Reason: "uuid_mismatch"})
		writeText(response, http.StatusOK, "ID_NOT_FOUND")
		return
	}
	now := time.Now().UTC()
	device.RustDeskID, device.ClientUUID = input.ID, input.UUID
	device.Hostname, device.Platform, device.Version = strings.TrimSpace(input.Hostname), strings.TrimSpace(input.OS), strings.TrimSpace(input.Version)
	device.CPU, device.Memory, device.Username = strings.TrimSpace(input.CPU), strings.TrimSpace(input.Memory), strings.TrimSpace(input.Username)
	device.LastSeenIP, device.Online, device.LastSeen = clientIP(request), true, now
	if device.CreatedAt.IsZero() {
		device.CreatedAt = now
	}
	if err := s.repository.UpsertDevice(request.Context(), device); err != nil {
		writeError(response, http.StatusInternalServerError, "device sysinfo update failed")
		return
	}
	if !exists {
		_ = s.audit.Record(request.Context(), domain.AuditEvent{Type: "device_discovered", TargetRustDeskID: input.ID,
			IP: clientIP(request), Result: "success", Metadata: map[string]any{"platform": device.Platform, "version": device.Version}})
	}
	writeText(response, http.StatusOK, "SYSINFO_UPDATED")
}

func (s *Server) clientSysinfoVersion(response http.ResponseWriter, _ *http.Request) {
	writeText(response, http.StatusOK, sysinfoVersion)
}

func (s *Server) inventoryDevice(request *http.Request, id, clientUUID string) (domain.Device, bool, bool, error) {
	devices, err := s.repository.ListDevices(request.Context())
	if err != nil {
		return domain.Device{}, false, false, err
	}
	for _, device := range devices {
		if device.RustDeskID != id {
			continue
		}
		return device, true, device.ClientUUID != "" && device.ClientUUID != clientUUID, nil
	}
	return domain.Device{}, false, false, nil
}

func validInventoryIdentity(id, clientUUID string) bool {
	id, clientUUID = strings.TrimSpace(id), strings.TrimSpace(clientUUID)
	return id != "" && clientUUID != "" && len(id) <= 64 && len(clientUUID) <= 512
}

func (s *Server) clientUsers(response http.ResponseWriter, request *http.Request) {
	principal, _ := principalFrom(request.Context())
	users, err := s.repository.ListUsers(request.Context())
	if err != nil {
		writeError(response, 500, "users unavailable")
		return
	}
	data := make([]map[string]any, 0, len(users))
	for _, user := range users {
		if principal.User.Role == domain.RoleAdmin || user.ID == principal.User.ID {
			data = append(data, clientUser(user))
		}
	}
	writeJSON(response, http.StatusOK, map[string]any{"total": len(data), "data": data})
}

func (s *Server) clientPeers(response http.ResponseWriter, request *http.Request) {
	principal, _ := principalFrom(request.Context())
	devices, err := s.accessibleDevices(request, principal.User)
	if err != nil {
		writeError(response, 500, "devices unavailable")
		return
	}
	users, _ := s.repository.ListUsers(request.Context())
	groups, _ := s.repository.ListGroups(request.Context())
	userNames, groupNames := map[string]string{}, map[string]string{}
	for _, user := range users {
		userNames[user.ID] = user.Username
	}
	for _, group := range groups {
		groupNames[group.ID] = group.Name
	}
	data := make([]map[string]any, 0, len(devices))
	for _, device := range devices {
		data = append(data, map[string]any{"id": device.RustDeskID, "info": map[string]string{
			"device_name": firstNonEmpty(device.Alias, device.Hostname), "os": device.Platform, "username": device.Username},
			"status": boolStatus(device.Online), "user": device.OwnerUserID, "user_name": userNames[device.OwnerUserID],
			"device_group_name": groupNames[device.GroupID], "note": ""})
	}
	writeJSON(response, http.StatusOK, map[string]any{"total": len(data), "data": data})
}

func (s *Server) clientDeviceGroups(response http.ResponseWriter, request *http.Request) {
	principal, _ := principalFrom(request.Context())
	devices, err := s.accessibleDevices(request, principal.User)
	if err != nil {
		writeError(response, 500, "device groups unavailable")
		return
	}
	allowed := map[string]bool{}
	for _, device := range devices {
		if device.GroupID != "" {
			allowed[device.GroupID] = true
		}
	}
	groups, err := s.repository.ListGroups(request.Context())
	if err != nil {
		writeError(response, 500, "device groups unavailable")
		return
	}
	data := make([]domain.Group, 0)
	for _, group := range groups {
		if group.Kind == domain.GroupKindDevice && (principal.User.Role == domain.RoleAdmin || allowed[group.ID]) {
			data = append(data, group)
		}
	}
	writeJSON(response, http.StatusOK, map[string]any{"total": len(data), "data": data})
}

func (s *Server) accessibleDevices(request *http.Request, user domain.User) ([]domain.Device, error) {
	devices, err := s.repository.ListDevices(request.Context())
	if err != nil {
		return nil, err
	}
	if user.Role == domain.RoleAdmin {
		return devices, nil
	}
	rules, err := s.repository.ListACLRules(request.Context())
	if err != nil {
		return nil, err
	}
	memberships, err := s.repository.ListUserGroupMemberships(request.Context())
	if err != nil {
		return nil, err
	}
	userGroups := map[string]bool{}
	for _, membership := range memberships {
		if membership.UserID == user.ID && membership.Active {
			userGroups[membership.GroupID] = true
		}
	}
	result := make([]domain.Device, 0)
	for _, device := range devices {
		if device.OwnerUserID == user.ID || inventoryACLAllows(rules, user.ID, userGroups, device) {
			result = append(result, device)
		}
	}
	return result, nil
}

func inventoryACLAllows(rules []domain.ACLRule, userID string, groups map[string]bool, device domain.Device) bool {
	for _, rule := range rules {
		if !rule.Enabled {
			continue
		}
		subject := rule.SubjectID == "" || rule.SubjectType == "user" && rule.SubjectID == userID || rule.SubjectType == "user_group" && groups[rule.SubjectID]
		target := rule.TargetID == "" || rule.TargetType == "device" && rule.TargetID == device.RustDeskID || rule.TargetType == "device_group" && device.GroupID != "" && rule.TargetID == device.GroupID
		if !subject || !target {
			continue
		}
		for _, permission := range rule.Permissions {
			if permission == "view" || permission == "remote_control" || permission == "manage" {
				return true
			}
		}
	}
	return false
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}

func writeText(response http.ResponseWriter, status int, value string) {
	response.Header().Set("Content-Type", "text/plain; charset=utf-8")
	response.WriteHeader(status)
	_, _ = response.Write([]byte(value))
}

// RustDesk adds optional inventory fields over time. Keep this decoder forward-compatible,
// while retaining the same strict body-size and single-value guarantees as other API inputs.
func decodeInventoryJSON(request *http.Request, target any, limit int64) error {
	limited := &io.LimitedReader{R: request.Body, N: limit + 1}
	decoder := json.NewDecoder(limited)
	if err := decoder.Decode(target); err != nil {
		return err
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		if err == nil {
			return errors.New("multiple JSON values")
		}
		return err
	}
	if limited.N <= 0 || request.ContentLength > limit {
		return errors.New("JSON body too large")
	}
	return nil
}
